import { serve } from "https://deno.land/std@0.168.0/http/server.ts"
import { createClient } from 'https://esm.sh/@supabase/supabase-js@2'

interface ModelUsageStats {
  model: string
  total_requests: number
  input_tokens: number
  output_tokens: number
  total_tokens: number
  total_cost: number
}

interface FunctionUsageStats {
  function_name: string
  total_requests: number
  input_tokens: number
  output_tokens: number
  total_tokens: number
  total_cost: number
  avg_response_time_ms: number
  success_rate: number
}

serve(async (req) => {
  const corsHeaders = {
    'Access-Control-Allow-Origin': '*',
    'Access-Control-Allow-Headers': 'authorization, x-client-info, apikey, content-type',
    'Access-Control-Allow-Methods': 'GET, OPTIONS',
  }

  if (req.method === 'OPTIONS') {
    return new Response(null, { status: 200, headers: corsHeaders })
  }

  if (req.method !== 'GET') {
    return new Response(
      JSON.stringify({ error: 'Method not allowed' }),
      { status: 405, headers: { ...corsHeaders, 'Content-Type': 'application/json' } }
    )
  }

  try {
    const supabaseUrl = Deno.env.get('SUPABASE_URL')!
    const supabaseServiceKey = Deno.env.get('SUPABASE_SERVICE_ROLE_KEY')!
    const supabase = createClient(supabaseUrl, supabaseServiceKey)

    const authHeader = req.headers.get('Authorization')
    
    if (!authHeader) {
      return new Response(
        JSON.stringify({ error: 'Unauthorized' }),
        { status: 401, headers: { ...corsHeaders, 'Content-Type': 'application/json' } }
      )
    }

    const token = authHeader.replace('Bearer ', '')
    const { data: { user }, error: authError } = await supabase.auth.getUser(token)
    
    if (authError || !user) {
      console.error('Authentication error:', authError)
      return new Response(
        JSON.stringify({ error: 'Unauthorized' }),
        { status: 401, headers: { ...corsHeaders, 'Content-Type': 'application/json' } }
      )
    }

    const { data: isAdmin, error: adminError } = await supabase.rpc('is_admin_user', {
      p_user_id: user.id
    })

    if (adminError || !isAdmin) {
      console.error('Admin check failed:', adminError)
      return new Response(
        JSON.stringify({ error: 'Forbidden: Admin access required' }),
        { status: 403, headers: { ...corsHeaders, 'Content-Type': 'application/json' } }
      )
    }

    const url = new URL(req.url)
    const period = url.searchParams.get('period') || 'all'
    const groupBy = url.searchParams.get('group_by') || 'model'

    // Validate period format if not 'all'
    if (period !== 'all' && !period.match(/^\d{4}-\d{2}$/)) {
      return new Response(
        JSON.stringify({ error: 'Invalid period format. Use YYYY-MM or "all"' }),
        { status: 400, headers: { ...corsHeaders, 'Content-Type': 'application/json' } }
      )
    }

    // Validate group_by parameter
    if (!['model', 'function'].includes(groupBy)) {
      return new Response(
        JSON.stringify({ error: 'Invalid group_by parameter. Use "model" or "function"' }),
        { status: 400, headers: { ...corsHeaders, 'Content-Type': 'application/json' } }
      )
    }

    const rpcFunction = groupBy === 'function' ? 'get_llm_usage_by_function' : 'get_llm_usage_by_model'
    const { data: stats, error } = await supabase.rpc(rpcFunction, {
      p_period: period
    })

    if (error) {
      console.error('Database error:', error)
      return new Response(
        JSON.stringify({ error: 'Failed to fetch usage data' }),
        { status: 500, headers: { ...corsHeaders, 'Content-Type': 'application/json' } }
      )
    }

    const response = {
      period,
      group_by: groupBy,
      data: stats || []
    }

    return new Response(
      JSON.stringify(response),
      { status: 200, headers: { ...corsHeaders, 'Content-Type': 'application/json' } }
    )

  } catch (error) {
    console.error('Usage stats error:', error)
    
    return new Response(
      JSON.stringify({ error: 'Internal server error' }),
      { status: 500, headers: { ...corsHeaders, 'Content-Type': 'application/json' } }
    )
  }
})
