import { serve } from "https://deno.land/std@0.168.0/http/server.ts"
import { createClient } from 'https://esm.sh/@supabase/supabase-js@2'

interface ChatMessage {
  role: 'system' | 'user' | 'assistant'
  content: string
}

interface ChatCompletionRequest {
  model: string
  messages: ChatMessage[]
  max_tokens?: number
  temperature?: number
  stream?: boolean
}

const rateLimitStore = new Map<string, { count: number; resetTime: number }>()

function checkRateLimit(userId: string, limit: number = 60, windowMs: number = 60000): boolean {
  const now = Date.now()
  const key = userId || 'anonymous'
  const userLimit = rateLimitStore.get(key)

  if (!userLimit || now > userLimit.resetTime) {
    rateLimitStore.set(key, { count: 1, resetTime: now + windowMs })
    return true
  }

  if (userLimit.count >= limit) {
    return false
  }

  userLimit.count++
  return true
}

serve(async (req) => {
  const startTime = Date.now()

  console.log(`[${new Date().toISOString()}] LLM-Proxy function called: ${req.method} ${req.url}`)

  const corsHeaders = {
    'Access-Control-Allow-Origin': '*',
    'Access-Control-Allow-Headers': 'authorization, x-client-info, apikey, content-type',
    'Access-Control-Allow-Methods': 'POST, OPTIONS',
  }

  if (req.method === 'OPTIONS') {
    return new Response(null, { status: 200, headers: corsHeaders })
  }

  if (req.method === 'GET' && req.url.includes('/logs')) {
    try {
      const logContent = await Deno.readTextFile('/tmp/llm-proxy.log')
      return new Response(logContent, {
        headers: { 
          ...corsHeaders, 
          'Content-Type': 'text/plain',
          'Content-Disposition': 'attachment; filename="llm-proxy.log"'
        }
      })
    } catch (error) {
      return new Response('Log file not found or empty', {
        status: 404,
        headers: { ...corsHeaders, 'Content-Type': 'text/plain' }
      })
    }
  }

  if (req.method !== 'POST') {
    return new Response(
      JSON.stringify({ error: 'Only POST requests are allowed' }),
      { status: 405, headers: { ...corsHeaders, 'Content-Type': 'application/json' } }
    )
  }

  try {
    const body = await req.json() as ChatCompletionRequest
    console.log(`[${new Date().toISOString()}] Processing request`)

    if (!body.messages || !Array.isArray(body.messages)) {
      return new Response(
        JSON.stringify({ error: 'messages field is required and must be an array' }),
        { status: 400, headers: { ...corsHeaders, 'Content-Type': 'application/json' } }
      )
    }

    const supabaseUrl = Deno.env.get('SUPABASE_URL')!
    const supabaseServiceKey = Deno.env.get('SUPABASE_SERVICE_ROLE_KEY')!
    const supabase = createClient(supabaseUrl, supabaseServiceKey)

    const authHeader = req.headers.get('Authorization')
    
    let userId = 'anonymous'
    
    if (authHeader) {
      try {
        const token = authHeader.replace('Bearer ', '')
        const { data: { user } } = await supabase.auth.getUser(token)
        userId = user?.id || 'anonymous'
      } catch (error) {
        console.error(`[${new Date().toISOString()}] DEBUG: Token validation failed:`, error)
        console.warn('Failed to get user from token:', error)
      }
    }

    if (!checkRateLimit(userId, 60, 60000)) {
      return new Response(
        JSON.stringify({ 
          error: 'Rate limit exceeded. Please try again later.',
          retry_after: 60
        }),
        { status: 429, headers: { ...corsHeaders, 'Content-Type': 'application/json' } }
      )
    }

    const chatConfig: ChatCompletionRequest = {
      model: body.model || 'gpt-4o-mini',
      messages: body.messages,
      max_tokens: body.max_tokens || 2000,
      temperature: body.temperature ?? 0.1,
      stream: body.stream ?? false,
    }

    const openaiApiKey = Deno.env.get('OPENAI_API_KEY')
    if (!openaiApiKey) {
      return new Response(
        JSON.stringify({ error: 'OpenAI API key not configured' }),
        { status: 500, headers: { ...corsHeaders, 'Content-Type': 'application/json' } }
      )
    }

    const openaiResponse = await fetch('https://api.openai.com/v1/chat/completions', {
      method: 'POST',
      headers: {
        'Authorization': `Bearer ${openaiApiKey}`,
        'Content-Type': 'application/json',
      },
      body: JSON.stringify(chatConfig),
    })

    const responseData = await openaiResponse.json()
    const responseTime = Date.now() - startTime

    try {
      const tokensUsed = responseData.usage?.total_tokens || 0
      const modelUsed = responseData.model || chatConfig.model
      
      const costs: Record<string, number> = {
        'gpt-3.5-turbo': 0.0005 / 1000,
        'gpt-4o-mini': 0.00015 / 1000,
        'gpt-4o': 0.005 / 1000,
      }
      const cost = tokensUsed * (costs[modelUsed] || 0.0005 / 1000)

      const { error: insertError } = await supabase
        .from('llm_usage_logs')
        .insert({
          user_id: userId,
          function_name: 'chat_completion',
          model_used: modelUsed,
          tokens_used: tokensUsed,
          cost: cost,
          response_time_ms: responseTime,
          success: openaiResponse.ok
        })
      
      if (insertError) {
        console.warn('Failed to insert usage log:', insertError)
      }
    } catch (error) {
      console.warn('Failed to log usage:', error)
    }

    return new Response(JSON.stringify(responseData), {
      status: openaiResponse.status,
      headers: {
        'Content-Type': 'application/json',
        'Access-Control-Allow-Origin': '*',
        'Access-Control-Allow-Headers': 'authorization, x-client-info, apikey, content-type',
      },
    })

  } catch (error) {
    console.error('LLM Proxy error:', error)
    
    return new Response(
      JSON.stringify({ error: 'Internal server error' }),
      { status: 500, headers: { ...corsHeaders, 'Content-Type': 'application/json' } }
    )
  }
})
