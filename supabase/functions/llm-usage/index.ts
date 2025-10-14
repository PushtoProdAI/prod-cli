import { serve } from "https://deno.land/std@0.168.0/http/server.ts"
import { createClient } from 'https://esm.sh/@supabase/supabase-js@2'

interface UsageStats {
  total_requests: number
  total_tokens: number
  total_cost: number
  average_latency: number
  requests_by_model: Record<string, number>
  cost_by_model: Record<string, number>
  requests_by_day: Record<string, number>
}

serve(async (req) => {
  // CORS headers
  const corsHeaders = {
    'Access-Control-Allow-Origin': '*',
    'Access-Control-Allow-Headers': 'authorization, x-client-info, apikey, content-type',
    'Access-Control-Allow-Methods': 'GET, OPTIONS',
  }

  // Handle preflight requests
  if (req.method === 'OPTIONS') {
    return new Response(null, { status: 200, headers: corsHeaders })
  }

  // Only allow GET requests
  if (req.method !== 'GET') {
    return new Response(
      JSON.stringify({ error: 'Method not allowed' }),
      { status: 405, headers: { ...corsHeaders, 'Content-Type': 'application/json' } }
    )
  }

  try {
    // Initialize Supabase client
    const supabaseUrl = Deno.env.get('SUPABASE_URL')!
    const supabaseServiceKey = Deno.env.get('SUPABASE_SERVICE_ROLE_KEY')!
    const supabase = createClient(supabaseUrl, supabaseServiceKey)

    // Get user ID from JWT token
    const authHeader = req.headers.get('Authorization')
    let userId = 'anonymous'
    
    if (authHeader) {
      try {
        const token = authHeader.replace('Bearer ', '')
        const { data: { user } } = await supabase.auth.getUser(token)
        userId = user?.id || 'anonymous'
      } catch (error) {
        console.warn('Failed to get user from token:', error)
      }
    }

    // Parse query parameters
    const url = new URL(req.url)
    const period = url.searchParams.get('period') || '30d'
    const days = parseInt(url.searchParams.get('days') || '30')

    // Calculate date range
    const endDate = new Date()
    const startDate = new Date()
    startDate.setDate(startDate.getDate() - days)

    // Query usage logs
    const { data: logs, error } = await supabase
      .from('llm_usage_logs')
      .select('*')
      .eq('user_id', userId)
      .gte('created_at', startDate.toISOString())
      .lte('created_at', endDate.toISOString())

    if (error) {
      console.error('Database error:', error)
      return new Response(
        JSON.stringify({ error: 'Failed to fetch usage data' }),
        { status: 500, headers: { ...corsHeaders, 'Content-Type': 'application/json' } }
      )
    }

    // Calculate statistics
    const stats: UsageStats = {
      total_requests: 0,
      total_tokens: 0,
      total_cost: 0,
      average_latency: 0,
      requests_by_model: {},
      cost_by_model: {},
      requests_by_day: {}
    }

    if (logs && logs.length > 0) {
      let totalLatency = 0
      
      for (const log of logs) {
        stats.total_requests++
        stats.total_tokens += log.tokens_used || 0
        stats.total_cost += log.cost || 0
        totalLatency += log.response_time_ms || 0

        // Group by model
        const model = log.model_used || 'unknown'
        stats.requests_by_model[model] = (stats.requests_by_model[model] || 0) + 1
        stats.cost_by_model[model] = (stats.cost_by_model[model] || 0) + (log.cost || 0)

        // Group by day
        const day = new Date(log.created_at).toISOString().split('T')[0]
        stats.requests_by_day[day] = (stats.requests_by_day[day] || 0) + 1
      }

      stats.average_latency = stats.total_requests > 0 ? totalLatency / stats.total_requests : 0
    }

    return new Response(
      JSON.stringify(stats),
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
