import "jsr:@supabase/functions-js/edge-runtime.d.ts"
import { createClient } from "https://esm.sh/@supabase/supabase-js@2"

interface UsageData {
  platform: string
  language: string
  serviceRequirements?: ServiceRequirement[] // Made optional
}

interface ServiceRequirement {
  type: string
  provider: string
}

Deno.serve(async (req) => {
  const { method } = req
  
  if (req.method !== 'POST' && req.method !== 'GET') {
    return new Response(JSON.stringify({ error: 'Method not allowed' }), {
      status: 405,
      headers: { 'Content-Type': 'application/json' },
    });
  }
 
  const supabase = createClient(
    Deno.env.get("SUPABASE_URL")!,
    Deno.env.get("SUPABASE_ANON_KEY"),
    {
      global: {
        headers: { Authorization: req.headers.get('Authorization')! },
      },
    }
  )

  // Handle GET requests - retrieve usage statistics
  if (req.method === 'GET') {
    const url = new URL(req.url)
    const platform = url.searchParams.get('platform')
    const language = url.searchParams.get('language')
    const serviceType = url.searchParams.get('service_type')
    const serviceProvider = url.searchParams.get('service_provider')

    try {
      let query = supabase
        .from('stack_usage')
        .select('*')

      if (platform) {
        query = query.eq('platform', platform.toLowerCase())
      }
      if (language) {
        query = query.eq('language', language.toLowerCase())
      }
      if (serviceType) {
        query = query.eq('service_type', serviceType.toLowerCase())
      }
      if (serviceProvider) {
        query = query.eq('service_provider', serviceProvider.toLowerCase())
      }

      const { data, error } = await query

      if (error) {
        console.error('Error retrieving usage stats:', error)
        return new Response(
          JSON.stringify({ error: 'Failed to retrieve usage stats' }),
          { status: 500, headers: { 'Content-Type': 'application/json' } }
        )
      }

      return new Response(
        JSON.stringify({ data }),
        { status: 200, headers: { 'Content-Type': 'application/json' } }
      )
    } catch (error) {
      console.error('Error in GET request:', error)
      return new Response(
        JSON.stringify({ error: 'Internal server error' }),
        { status: 500, headers: { 'Content-Type': 'application/json' } }
      )
    }
  }

  // Handle POST requests - record usage data
  let usageData: UsageData
  try {
    usageData = await req.json()
  } catch {
    return new Response(
      JSON.stringify({ error: 'Invalid JSON' }),
      { status: 400, headers: { 'Content-Type': 'application/json' } }
    )
  }

  if (!usageData.language || !usageData.platform) {
    return new Response(
      JSON.stringify({ error: 'Language and platform are required' }),
      { status: 400, headers: { 'Content-Type': 'application/json' } }
    )
  }

  // If no service requirements, create a default entry
  const servicesToProcess = usageData.serviceRequirements && usageData.serviceRequirements.length > 0
    ? usageData.serviceRequirements
    : [{ type: 'none', provider: 'none' }] // Default service for basic usage of just a web app

  for (const service of servicesToProcess) {
    const { error } = await updateUsageStats(
      supabase,
      usageData.platform,
      usageData.language,
      service
    )
    
    if (error) {
      console.error('Error updating usage stats:', error)
      return new Response(
        JSON.stringify({ error: 'Failed to update usage stats' }),
        { status: 500, headers: { 'Content-Type': 'application/json' } }
      )
    }
  }

  return new Response(
    JSON.stringify({
      success: true,
      message: 'Usage stats updated successfully'
    }),
    { status: 200, headers: { 'Content-Type': 'application/json' } }
  )
})

async function updateUsageStats(
  supabase: ReturnType<typeof createClient>,
  platform: string,
  language: string,
  service: ServiceRequirement
): Promise<{ error: unknown | null }> {
  const { error } = await supabase.rpc('increment_requested_stack_usage', {
    p_platform: platform.toLowerCase(),
    p_language: language.toLowerCase(),
    p_service_type: service.type.toLowerCase(),
    p_service_provider: service.provider.toLowerCase()
  })
  return { error }
}
