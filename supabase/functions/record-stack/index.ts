import "jsr:@supabase/functions-js/edge-runtime.d.ts"
import { createClient } from "https://esm.sh/@supabase/supabase-js@2"
import { initSentry, captureException, flushSentry } from '../_shared/sentry.ts';

// Initialize Sentry
initSentry();

interface UsageData {
  platform: string
  language: string
  serviceRequirements?: ServiceRequirement[] // Made optional
}

interface ServiceRequirement {
  type: string
  provider: string
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
  
  if (req.method !== 'POST' && req.method !== 'GET') {
    return new Response(JSON.stringify({ error: 'Method not allowed' }), {
      status: 405,
      headers: { ...corsHeaders, 'Content-Type': 'application/json' },
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
      const { data, error } = await supabase.rpc('get_stack_usage_stats', {
        p_platform: platform?.toLowerCase() || null,
        p_language: language?.toLowerCase() || null,
        p_service_type: serviceType?.toLowerCase() || null,
        p_service_provider: serviceProvider?.toLowerCase() || null
      })

      if (error) {
        console.error('Error retrieving usage stats:', error)
        captureException(new Error(String(error)), {
          function: 'record-stack',
          operation: 'get_usage_stats',
          platform,
          language,
          service_type: serviceType,
          service_provider: serviceProvider
        });
        return new Response(
          JSON.stringify({ error: 'Failed to retrieve usage stats' }),
          { status: 500, headers: { ...corsHeaders, 'Content-Type': 'application/json' } }
        )
      }

      return new Response(
        JSON.stringify({ data }),
        { status: 200, headers: { ...corsHeaders, 'Content-Type': 'application/json' } }
      )
    } catch (error) {
      console.error('Error in GET request:', error)
      captureException(error instanceof Error ? error : new Error(String(error)), {
        function: 'record-stack',
        operation: 'get_request_error',
        platform,
        language
      });
      return new Response(
        JSON.stringify({ error: 'Internal server error' }),
        { status: 500, headers: { ...corsHeaders, 'Content-Type': 'application/json' } }
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
      { status: 400, headers: { ...corsHeaders, 'Content-Type': 'application/json' } }
    )
  }

  if (!usageData.language || !usageData.platform) {
    return new Response(
      JSON.stringify({ error: 'Language and platform are required' }),
      { status: 400, headers: { ...corsHeaders, 'Content-Type': 'application/json' } }
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
      captureException(new Error(String(error) || 'Unknown database error'), {
        function: 'record-stack',
        operation: 'update_usage_stats',
        platform: usageData.platform,
        language: usageData.language,
        service_type: service.type,
        service_provider: service.provider
      });
      return new Response(
        JSON.stringify({ error: 'Failed to update usage stats' }),
        { status: 500, headers: { ...corsHeaders, 'Content-Type': 'application/json' } }
      )
    }
  }

  return new Response(
    JSON.stringify({
      success: true,
      message: 'Usage stats updated successfully'
    }),
    { status: 200, headers: { ...corsHeaders, 'Content-Type': 'application/json' } }
  )
  
  } catch (error) {
    console.error('Unexpected error in record-stack function:', error);
    captureException(error instanceof Error ? error : new Error(String(error)), {
      function: 'record-stack',
      operation: 'general_error',
      method: req.method
    });
    await flushSentry();
    
    return new Response(
      JSON.stringify({ error: 'Internal server error' }),
      { status: 500, headers: { ...corsHeaders, 'Content-Type': 'application/json' } }
    );
  }
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
