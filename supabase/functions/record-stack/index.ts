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

const supabase = createClient(
  Deno.env.get("SUPABASE_URL")!,
  Deno.env.get("SUPABASE_SERVICE_ROLE_KEY")!
)

Deno.serve(async (req) => {
  const { method } = req
  
  if (method !== 'POST') {
    return new Response("", { status: 405 })
  }

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
