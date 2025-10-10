import "jsr:@supabase/functions-js/edge-runtime.d.ts"
import { createClient } from "https://esm.sh/@supabase/supabase-js@2"
import Stripe from "https://esm.sh/stripe@14.21.0?target=deno"
import { initSentry, captureException, flushSentry } from '../_shared/sentry.ts'

// Initialize Sentry
initSentry()

const stripe = new Stripe(Deno.env.get("STRIPE_SECRET_KEY")!, {
  apiVersion: "2024-10-28.acacia",
  httpClient: Stripe.createFetchHttpClient(),
})

const webhookSecret = Deno.env.get("STRIPE_WEBHOOK_SECRET")!

Deno.serve(async (req) => {
  try {
    // Only accept POST requests
    if (req.method !== 'POST') {
      return new Response('Method not allowed', { status: 405 })
    }

    // Get the webhook signature from headers
    const signature = req.headers.get("stripe-signature")
    if (!signature) {
      console.error("No Stripe signature found in headers")
      return new Response('No signature', { status: 400 })
    }

    // Get raw body for signature verification
    const body = await req.text()

    // Verify webhook signature
    let event: Stripe.Event
    try {
      event = await stripe.webhooks.constructEventAsync(
        body,
        signature,
        webhookSecret
      )
    } catch (err) {
      console.error(`Webhook signature verification failed: ${err.message}`)
      captureException(err)
      await flushSentry()
      return new Response(`Webhook Error: ${err.message}`, { status: 400 })
    }

    console.log(`Processing webhook event: ${event.type}`)

    // Create Supabase client with service role (bypasses RLS)
    // Safe because webhook signature proves this is from Stripe
    const supabase = createClient(
      Deno.env.get("SUPABASE_URL")!,
      Deno.env.get("SUPABASE_SERVICE_ROLE_KEY")!
    )

    // Handle the event
    switch (event.type) {
      case 'checkout.session.completed': {
        const session = event.data.object as Stripe.Checkout.Session
        await handleCheckoutCompleted(supabase, session)
        break
      }

      case 'payment_intent.succeeded': {
        const paymentIntent = event.data.object as Stripe.PaymentIntent
        console.log(`PaymentIntent ${paymentIntent.id} succeeded`)
        // Additional handling if needed
        break
      }

      case 'payment_intent.payment_failed': {
        const paymentIntent = event.data.object as Stripe.PaymentIntent
        console.error(`PaymentIntent ${paymentIntent.id} failed`)
        // Could log failed payments or notify user
        break
      }

      default:
        console.log(`Unhandled event type: ${event.type}`)
    }

    return new Response(JSON.stringify({ received: true }), {
      status: 200,
      headers: { 'Content-Type': 'application/json' }
    })

  } catch (error) {
    console.error('Webhook handler error:', error)
    captureException(error)
    await flushSentry()
    return new Response(
      JSON.stringify({ error: error.message }),
      { status: 500, headers: { 'Content-Type': 'application/json' } }
    )
  }
})

async function handleCheckoutCompleted(
  supabase: any,
  session: Stripe.Checkout.Session
) {
  console.log(`Processing checkout session: ${session.id}`)

  // Extract metadata
  const userId = session.metadata?.user_id
  const packageId = session.metadata?.package_id

  if (!userId || !packageId) {
    console.error('Missing user_id or package_id in session metadata')
    throw new Error('Invalid session metadata')
  }

  // Get package details to know how many tokens to add
  const { data: tokenPackage, error: packageError } = await supabase
    .from('token_packages')
    .select('*')
    .eq('id', packageId)
    .single()

  if (packageError || !tokenPackage) {
    console.error('Failed to fetch token package:', packageError)
    throw new Error(`Package not found: ${packageId}`)
  }

  console.log(`Adding ${tokenPackage.token_count} tokens for user ${userId}`)

  // Check if purchase already exists (idempotency)
  const { data: existingPurchase } = await supabase
    .from('token_purchases')
    .select('id')
    .eq('payment_id', session.payment_intent)
    .single()

  if (existingPurchase) {
    console.log(`Purchase already processed: ${session.payment_intent}`)
    return // Already processed, skip
  }

  // Start transaction-like operations
  // 1. Create purchase record
  const { data: purchase, error: purchaseError } = await supabase
    .from('token_purchases')
    .insert({
      user_id: userId,
      package_id: packageId,
      price_paid_cents: tokenPackage.price_cents,
      tokens_purchased: tokenPackage.token_count,
      payment_id: session.payment_intent,
      payment_provider: 'stripe',
      payment_status: 'completed',
      metadata: {
        session_id: session.id,
        customer_email: session.customer_email,
      }
    })
    .select()
    .single()

  if (purchaseError) {
    console.error('Failed to create purchase record:', purchaseError)
    throw purchaseError
  }

  console.log(`Created purchase record: ${purchase.id}`)

  // 2. Update token balance (add bonus tokens to purchased tokens)
  const { data: balance, error: balanceError } = await supabase
    .from('token_balances')
    .select('*')
    .eq('user_id', userId)
    .single()

  if (balanceError || !balance) {
    // Create initial balance if doesn't exist
    const { error: createError } = await supabase
      .from('token_balances')
      .insert({
        user_id: userId,
        plan_tokens: 0,
        bonus_tokens: tokenPackage.token_count,
        used_tokens: 0,
        reset_date: new Date(Date.now() + 365 * 24 * 60 * 60 * 1000).toISOString(), // 1 year
      })

    if (createError) {
      console.error('Failed to create token balance:', createError)
      throw createError
    }

    console.log(`Created new token balance with ${tokenPackage.token_count} bonus tokens`)
  } else {
    // Add tokens to existing balance
    const { error: updateError } = await supabase
      .from('token_balances')
      .update({
        bonus_tokens: balance.bonus_tokens + tokenPackage.token_count,
        updated_at: new Date().toISOString(),
      })
      .eq('user_id', userId)

    if (updateError) {
      console.error('Failed to update token balance:', updateError)
      throw updateError
    }

    console.log(`Updated balance: ${balance.bonus_tokens} -> ${balance.bonus_tokens + tokenPackage.token_count} bonus tokens`)
  }

  console.log(`Successfully processed purchase for user ${userId}`)
}
