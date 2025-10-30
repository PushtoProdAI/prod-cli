import "jsr:@supabase/functions-js/edge-runtime.d.ts"
import { createClient } from "https://esm.sh/@supabase/supabase-js@2"
import { initSentry, captureException, flushSentry } from '../_shared/sentry.ts'

// Initialize Sentry
initSentry()

interface ConsumeRequest {
  amount: number
  operation: string
  metadata?: Record<string, any>
}

interface ConsumeResponse {
  success: boolean
  transaction_id?: string
  tokens_remaining?: number
  error_message?: string
}

interface TokenSummaryResponse {
  plan_tokens: number
  bonus_tokens: number
  used_tokens: number
  available_tokens: number
  reset_date: string
  usage_percentage: number
  recent_transactions: any[]
}

const corsHeaders = {
  'Access-Control-Allow-Origin': '*',
  'Access-Control-Allow-Headers': 'authorization, x-client-info, apikey, content-type',
  'Access-Control-Allow-Methods': 'GET, POST, OPTIONS',
  'Access-Control-Max-Age': '86400',
}

Deno.serve(async (req) => {
  try {
    // Handle CORS preflight requests
    if (req.method === 'OPTIONS') {
      return new Response('ok', { headers: corsHeaders })
    }

    // Create Supabase client with user's auth
    const supabase = createClient(
      Deno.env.get("SUPABASE_URL")!,
      Deno.env.get("SUPABASE_ANON_KEY")!,
      {
        global: {
          headers: { Authorization: req.headers.get('Authorization')! },
        },
      }
    )

    // Get authenticated user
    const { data: { user }, error: authError } = await supabase.auth.getUser()

    if (authError || !user) {
      return new Response(
        JSON.stringify({ error: 'Unauthorized' }),
        { status: 401, headers: { ...corsHeaders, 'Content-Type': 'application/json' } }
      )
    }

    const url = new URL(req.url)
    const pathname = url.pathname

    // Route: GET /tokens - Get token summary
    if (req.method === 'GET' && pathname.endsWith('/tokens')) {
      try {
        const { data, error } = await supabase.rpc('get_token_summary', {
          p_user_id: user.id
        })

        if (error) {
          console.error('Error getting token summary:', error)
          captureException(new Error(String(error)), {
            function: 'tokens',
            operation: 'get_summary',
            user_id: user.id
          })
          return new Response(
            JSON.stringify({ error: 'Failed to get token summary' }),
            { status: 500, headers: { ...corsHeaders, 'Content-Type': 'application/json' } }
          )
        }

        // Data comes back as array with single element
        const summary: TokenSummaryResponse = Array.isArray(data) && data.length > 0 ? data[0] : data

        return new Response(
          JSON.stringify(summary),
          { status: 200, headers: { ...corsHeaders, 'Content-Type': 'application/json' } }
        )
      } catch (error) {
        console.error('Error in GET /tokens:', error)
        captureException(error instanceof Error ? error : new Error(String(error)), {
          function: 'tokens',
          operation: 'get_summary_error',
          user_id: user.id
        })
        return new Response(
          JSON.stringify({ error: 'Internal server error' }),
          { status: 500, headers: { ...corsHeaders, 'Content-Type': 'application/json' } }
        )
      }
    }

    // Route: GET /tokens/balance - Get available token count (quick check)
    if (req.method === 'GET' && pathname.endsWith('/tokens/balance')) {
      try {
        const { data, error } = await supabase.rpc('get_available_tokens', {
          p_user_id: user.id
        })

        if (error) {
          console.error('Error getting available tokens:', error)
          captureException(new Error(String(error)), {
            function: 'tokens',
            operation: 'get_balance',
            user_id: user.id
          })
          return new Response(
            JSON.stringify({ error: 'Failed to get token balance' }),
            { status: 500, headers: { ...corsHeaders, 'Content-Type': 'application/json' } }
          )
        }

        // RPC returns array, get first element
        const available = Array.isArray(data) && data.length > 0 ? data[0] : data

        return new Response(
          JSON.stringify({ available_tokens: available }),
          { status: 200, headers: { ...corsHeaders, 'Content-Type': 'application/json' } }
        )
      } catch (error) {
        console.error('Error in GET /tokens/balance:', error)
        captureException(error instanceof Error ? error : new Error(String(error)), {
          function: 'tokens',
          operation: 'get_balance_error',
          user_id: user.id
        })
        return new Response(
          JSON.stringify({ error: 'Internal server error' }),
          { status: 500, headers: { ...corsHeaders, 'Content-Type': 'application/json' } }
        )
      }
    }

    // Route: POST /tokens/consume - Consume tokens
    if (req.method === 'POST' && pathname.endsWith('/tokens/consume')) {
      let requestBody: ConsumeRequest
      try {
        requestBody = await req.json()
      } catch {
        return new Response(
          JSON.stringify({ error: 'Invalid JSON' }),
          { status: 400, headers: { ...corsHeaders, 'Content-Type': 'application/json' } }
        )
      }

      // Validate request
      if (!requestBody.amount || requestBody.amount <= 0) {
        return new Response(
          JSON.stringify({ error: 'Invalid amount: must be positive' }),
          { status: 400, headers: { ...corsHeaders, 'Content-Type': 'application/json' } }
        )
      }

      if (!requestBody.operation) {
        return new Response(
          JSON.stringify({ error: 'Operation is required' }),
          { status: 400, headers: { ...corsHeaders, 'Content-Type': 'application/json' } }
        )
      }

      try {
        // Call database function to consume tokens atomically
        const { data, error } = await supabase.rpc('consume_tokens', {
          p_user_id: user.id,
          p_tokens_to_consume: requestBody.amount,
          p_operation: requestBody.operation,
          p_metadata: JSON.stringify(requestBody.metadata || {})
        })

        if (error) {
          console.error('Error consuming tokens:', error)
          captureException(new Error(String(error)), {
            function: 'tokens',
            operation: 'consume',
            user_id: user.id,
            amount: requestBody.amount,
            op_type: requestBody.operation
          })
          return new Response(
            JSON.stringify({ error: 'Failed to consume tokens' }),
            { status: 500, headers: { ...corsHeaders, 'Content-Type': 'application/json' } }
          )
        }

        // Data comes back as array with single row
        const result: ConsumeResponse = Array.isArray(data) && data.length > 0 ? data[0] : data

        // If consumption failed (insufficient tokens, etc.), return 400
        if (!result.success) {
          return new Response(
            JSON.stringify(result),
            { status: 400, headers: { ...corsHeaders, 'Content-Type': 'application/json' } }
          )
        }

        return new Response(
          JSON.stringify(result),
          { status: 200, headers: { ...corsHeaders, 'Content-Type': 'application/json' } }
        )
      } catch (error) {
        console.error('Error in POST /tokens/consume:', error)
        captureException(error instanceof Error ? error : new Error(String(error)), {
          function: 'tokens',
          operation: 'consume_error',
          user_id: user.id,
          amount: requestBody.amount
        })
        return new Response(
          JSON.stringify({ error: 'Internal server error' }),
          { status: 500, headers: { ...corsHeaders, 'Content-Type': 'application/json' } }
        )
      }
    }

    // Route: GET /tokens/total-used - Get total used tokens (admin only)
    if (req.method === 'GET' && pathname.endsWith('/tokens/total-used')) {
      try {
        // Create service role client for admin check
        const supabaseService = createClient(
          Deno.env.get("SUPABASE_URL")!,
          Deno.env.get("SUPABASE_SERVICE_ROLE_KEY")!
        )

        // Check if user is admin
        const { data: isAdmin, error: adminError } = await supabaseService.rpc('is_admin_user', {
          p_user_id: user.id
        })

        if (adminError || !isAdmin) {
          console.error('Admin check failed:', adminError)
          return new Response(
            JSON.stringify({ error: 'Forbidden: Admin access required' }),
            { status: 403, headers: { ...corsHeaders, 'Content-Type': 'application/json' } }
          )
        }

        // Get total used tokens using RPC
        const { data: totalUsed, error } = await supabaseService.rpc('get_total_used_tokens')

        if (error) {
          console.error('Error getting total used tokens:', error)
          captureException(new Error(String(error)), {
            function: 'tokens',
            operation: 'get_total_used',
            user_id: user.id
          })
          return new Response(
            JSON.stringify({ error: 'Failed to get total used tokens' }),
            { status: 500, headers: { ...corsHeaders, 'Content-Type': 'application/json' } }
          )
        }

        return new Response(
          JSON.stringify({ total_used_tokens: totalUsed }),
          { status: 200, headers: { ...corsHeaders, 'Content-Type': 'application/json' } }
        )
      } catch (error) {
        console.error('Error in GET /tokens/total-used:', error)
        captureException(error instanceof Error ? error : new Error(String(error)), {
          function: 'tokens',
          operation: 'get_total_used_error',
          user_id: user.id
        })
        return new Response(
          JSON.stringify({ error: 'Internal server error' }),
          { status: 500, headers: { ...corsHeaders, 'Content-Type': 'application/json' } }
        )
      }
    }

    // Route: GET /tokens/packages - Get available token packages for purchase
    if (req.method === 'GET' && pathname.endsWith('/tokens/packages')) {
      try {
        const { data, error } = await supabase
          .from('token_packages')
          .select('*')
          .eq('active', true)
          .order('sort_order', { ascending: true })

        if (error) {
          console.error('Error getting token packages:', error)
          captureException(new Error(String(error)), {
            function: 'tokens',
            operation: 'get_packages',
            user_id: user.id
          })
          return new Response(
            JSON.stringify({ error: 'Failed to get token packages' }),
            { status: 500, headers: { ...corsHeaders, 'Content-Type': 'application/json' } }
          )
        }

        return new Response(
          JSON.stringify({ packages: data }),
          { status: 200, headers: { ...corsHeaders, 'Content-Type': 'application/json' } }
        )
      } catch (error) {
        console.error('Error in GET /tokens/packages:', error)
        captureException(error instanceof Error ? error : new Error(String(error)), {
          function: 'tokens',
          operation: 'get_packages_error',
          user_id: user.id
        })
        return new Response(
          JSON.stringify({ error: 'Internal server error' }),
          { status: 500, headers: { ...corsHeaders, 'Content-Type': 'application/json' } }
        )
      }
    }

    // Unknown route
    return new Response(
      JSON.stringify({ error: 'Not found' }),
      { status: 404, headers: { ...corsHeaders, 'Content-Type': 'application/json' } }
    )

  } catch (error) {
    console.error('Unexpected error in tokens function:', error)
    captureException(error instanceof Error ? error : new Error(String(error)), {
      function: 'tokens',
      operation: 'general_error',
      method: req.method
    })
    await flushSentry()

    return new Response(
      JSON.stringify({ error: 'Internal server error' }),
      { status: 500, headers: { ...corsHeaders, 'Content-Type': 'application/json' } }
    )
  }
})
