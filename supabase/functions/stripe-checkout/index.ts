import "jsr:@supabase/functions-js/edge-runtime.d.ts";
import { createClient } from "https://esm.sh/@supabase/supabase-js@2";
import Stripe from "https://esm.sh/stripe@14.21.0?target=deno";
import {
  initSentry,
  captureException,
  flushSentry,
} from "../_shared/sentry.ts";

// Initialize Sentry
initSentry();

const stripe = new Stripe(Deno.env.get("STRIPE_SECRET_KEY")!, {
  apiVersion: "2024-10-28.acacia",
  httpClient: Stripe.createFetchHttpClient(),
});

const corsHeaders = {
  "Access-Control-Allow-Origin": "*",
  "Access-Control-Allow-Headers":
    "authorization, x-client-info, apikey, content-type",
  "Access-Control-Allow-Methods": "POST, OPTIONS",
  "Access-Control-Max-Age": "86400",
};

interface CreateCheckoutRequest {
  package_id: string;
  success_url?: string;
  cancel_url?: string;
}

Deno.serve(async (req) => {
  try {
    // Handle CORS preflight
    if (req.method === "OPTIONS") {
      return new Response("ok", { headers: corsHeaders });
    }

    // Only accept POST
    if (req.method !== "POST") {
      return new Response("Method not allowed", {
        status: 405,
        headers: corsHeaders,
      });
    }

    // Create Supabase client with user's auth
    const supabase = createClient(
      Deno.env.get("SUPABASE_URL")!,
      Deno.env.get("SUPABASE_ANON_KEY")!,
      {
        global: {
          headers: { Authorization: req.headers.get("Authorization")! },
        },
      }
    );

    // Get authenticated user
    const {
      data: { user },
      error: authError,
    } = await supabase.auth.getUser();

    if (authError || !user) {
      console.error("Authentication failed:", authError);
      return new Response(JSON.stringify({ error: "Unauthorized" }), {
        status: 401,
        headers: { ...corsHeaders, "Content-Type": "application/json" },
      });
    }

    console.log(`Creating checkout session for user: ${user.id}`);

    // Parse request body
    const body: CreateCheckoutRequest = await req.json();
    const { package_id, success_url, cancel_url } = body;

    if (!package_id) {
      return new Response(JSON.stringify({ error: "package_id is required" }), {
        status: 400,
        headers: { ...corsHeaders, "Content-Type": "application/json" },
      });
    }

    // Fetch token package from database
    const { data: tokenPackage, error: packageError } = await supabase
      .from("token_packages")
      .select("*")
      .eq("id", package_id)
      .eq("active", true)
      .single();

    if (packageError || !tokenPackage) {
      console.error("Token package not found:", packageError);
      return new Response(JSON.stringify({ error: "Invalid package_id" }), {
        status: 404,
        headers: { ...corsHeaders, "Content-Type": "application/json" },
      });
    }

    // Get Stripe Price ID (from DB or fallback for local dev only)
    let stripePriceId = tokenPackage.stripe_price_id;

    // Require Stripe Price ID for production and staging
    if (!stripePriceId) {
      const environment = Deno.env.get("ENVIRONMENT") || "local";
      const requiresStripeId = environment === "production" || environment === "staging";

      if (requiresStripeId) {
        console.error(
          `Package missing stripe_price_id in ${environment}:`,
          package_id
        );
        return new Response(
          JSON.stringify({
            error: "Package not configured for purchase. Please set Stripe IDs in database."
          }),
          {
            status: 500,
            headers: { ...corsHeaders, "Content-Type": "application/json" },
          }
        );
      }

      // Fallback to test prices for local development only
      const testPrices: Record<string, string> = {
        Starter:
          Deno.env.get("STRIPE_PRICE_STARTER_TEST") ||
          "price_1SIZrPRVwIwdESDmV06jXNFY",
        Builder:
          Deno.env.get("STRIPE_PRICE_BUILDER_TEST") ||
          "price_1SIZreRVwIwdESDmulL0Rpl1",
      };

      stripePriceId = testPrices[tokenPackage.name];
      console.log(
        `Using test price for ${tokenPackage.name} (local dev): ${stripePriceId}`
      );
    }

    console.log(
      `Creating checkout for package: ${tokenPackage.name} (${tokenPackage.token_count} tokens)`
    );

    // Create Stripe Checkout Session
    // For CLI usage without a web app, we can use Stripe's hosted success page
    // by omitting the success_url/cancel_url, OR we can provide custom URLs
    const sessionConfig: Stripe.Checkout.SessionCreateParams = {
      mode: "payment",
      payment_method_types: ["card"],
      line_items: [
        {
          price: stripePriceId,
          quantity: 1,
        },
      ],
      customer_email: user.email,
      metadata: {
        user_id: user.id,
        package_id: package_id,
      },
      automatic_tax: { enabled: false },
    };

    // Only add success/cancel URLs if provided (otherwise Stripe shows its own success page)
    if (success_url) {
      sessionConfig.success_url = success_url;
    }
    if (cancel_url) {
      sessionConfig.cancel_url = cancel_url;
    }

    const session = await stripe.checkout.sessions.create(sessionConfig);

    console.log(`Checkout session created: ${session.id}`);

    return new Response(
      JSON.stringify({
        session_id: session.id,
        url: session.url,
        package: {
          name: tokenPackage.name,
          tokens: tokenPackage.token_count,
          price_cents: tokenPackage.price_cents,
        },
      }),
      {
        status: 200,
        headers: { ...corsHeaders, "Content-Type": "application/json" },
      }
    );
  } catch (error) {
    console.error("Checkout creation error:", error);
    captureException(error);
    await flushSentry();

    return new Response(JSON.stringify({ error: error.message }), {
      status: 500,
      headers: { ...corsHeaders, "Content-Type": "application/json" },
    });
  }
});
