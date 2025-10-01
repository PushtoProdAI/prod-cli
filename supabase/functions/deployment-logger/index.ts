import "jsr:@supabase/functions-js/edge-runtime.d.ts"
import { createClient } from "https://esm.sh/@supabase/supabase-js@2"

interface DeploymentOperation {
  user_id?: string
  operation_type: string // 'deploy', 'rollback', 'scale', 'delete', 'update'
  resource_type: string // 'app', 'service', 'stack', 'container'
  resource_id: string
  resource_name?: string
  status: 'started' | 'success' | 'failed' | 'cancelled'
  platform?: string // 'flyio', 'netlify', 'vercel', 'heroku', 'render'
  language?: string // 'nodejs', 'python', 'go', 'rust', etc.
  service_type?: string // 'database', 'redis', 'storage', etc.
  service_provider?: string // 'postgres', 'redis', 's3', etc.
  deployment_config?: Record<string, any>
  error_message?: string
  metadata?: Record<string, any>
}

interface UpdateDeploymentOperation {
  operation_id: string
  status: 'success' | 'failed' | 'cancelled'
  error_message?: string
  metadata?: Record<string, any>
}

const corsHeaders = {
  'Access-Control-Allow-Origin': '*',
  'Access-Control-Allow-Headers': 'authorization, x-client-info, apikey, content-type',
  'Access-Control-Allow-Methods': 'POST, OPTIONS',
  'Access-Control-Max-Age': '86400',
}

Deno.serve(async (req) => {
  // Handle CORS preflight requests
  if (req.method === 'OPTIONS') {
    return new Response('ok', { headers: corsHeaders })
  }

  if (req.method !== 'POST') {
    return new Response(
      JSON.stringify({ error: 'Method not allowed' }),
      { status: 405, headers: { ...corsHeaders, 'Content-Type': 'application/json' } }
    )
  }

  const supabase = createClient(
    Deno.env.get('SUPABASE_URL')!,
    Deno.env.get('SUPABASE_SERVICE_ROLE_KEY')!,
    {
      global: {
        headers: { Authorization: req.headers.get('Authorization')! },
      },
    }
  )

  try {
    const body = await req.json()
    const { action, data } = body

    if (!action || !data) {
      return new Response(
        JSON.stringify({ error: 'action and data are required' }),
        { status: 400, headers: { ...corsHeaders, 'Content-Type': 'application/json' } }
      )
    }

    // Extract client information
    const ip_address = req.headers.get('x-forwarded-for') || 
                     req.headers.get('x-real-ip') || 
                     'unknown'
    const user_agent = req.headers.get('user-agent') || 'unknown'

    let result

    switch (action) {
      case 'log_deployment':
        result = await logDeploymentOperation(supabase, data as DeploymentOperation, ip_address, user_agent)
        break
      
      case 'update_deployment':
        result = await updateDeploymentOperation(supabase, data as UpdateDeploymentOperation)
        break
      
      default:
        return new Response(
          JSON.stringify({ error: 'Invalid action. Must be log_deployment or update_deployment' }),
          { status: 400, headers: { ...corsHeaders, 'Content-Type': 'application/json' } }
        )
    }

    return new Response(
      JSON.stringify({ success: true, data: result }),
      { status: 200, headers: { ...corsHeaders, 'Content-Type': 'application/json' } }
    )

  } catch (error) {
    console.error('Deployment logging error:', error)
    return new Response(
      JSON.stringify({ 
        success: false,
        error: 'Failed to log deployment operation',
        message: error instanceof Error ? error.message : 'Unknown error'
      }),
      { status: 500, headers: { ...corsHeaders, 'Content-Type': 'application/json' } }
    )
  }
})

async function logDeploymentOperation(
  supabase: any,
  operation: DeploymentOperation,
  ip_address: string,
  user_agent: string
): Promise<string> {
  const { data, error } = await supabase.rpc('log_deployment_operation', {
    p_user_id: operation.user_id || null,
    p_operation_type: operation.operation_type,
    p_resource_type: operation.resource_type,
    p_resource_id: operation.resource_id,
    p_resource_name: operation.resource_name || null,
    p_status: operation.status,
    p_platform: operation.platform || null,
    p_language: operation.language || null,
    p_service_type: operation.service_type || null,
    p_service_provider: operation.service_provider || null,
    p_deployment_config: operation.deployment_config || null,
    p_error_message: operation.error_message || null,
    p_ip_address: ip_address,
    p_user_agent: user_agent,
    p_metadata: operation.metadata || null
  })

  if (error) {
    throw new Error(`Failed to log deployment operation: ${error.message}`)
  }

  return data
}

async function updateDeploymentOperation(
  supabase: any,
  update: UpdateDeploymentOperation
): Promise<void> {
  const { error } = await supabase.rpc('update_deployment_operation', {
    p_operation_id: update.operation_id,
    p_status: update.status,
    p_error_message: update.error_message || null,
    p_metadata: update.metadata || null
  })

  if (error) {
    throw new Error(`Failed to update deployment operation: ${error.message}`)
  }
}
