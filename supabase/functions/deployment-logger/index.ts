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
  'Access-Control-Allow-Methods': 'GET, POST, OPTIONS',
  'Access-Control-Max-Age': '86400',
}

async function handleGetDeployments(req: Request, supabase: any, accessToken: string) {
  try {
    const { data: { user }, error: authError } = await supabase.auth.getUser(accessToken)
    
    if (authError || !user) {
      console.error('Authentication error:', authError)
      return new Response(
        JSON.stringify({ error: 'Unauthorized' }),
        { status: 401, headers: { ...corsHeaders, 'Content-Type': 'application/json' } }
      )
    }

    // Check if user is admin
    const { data: isAdmin, error: adminError } = await supabase.rpc('is_admin_user', {
      p_user_id: user.id
    })

    if (adminError) {
      console.error('Admin check failed:', adminError)
    }

    const url = new URL(req.url)
    
    // Parse query parameters
    const resourceName = url.searchParams.get('resource_name') || undefined
    const platform = url.searchParams.get('platform') || undefined
    const status = url.searchParams.get('status') || undefined
    const operationType = url.searchParams.get('operation_type') || undefined
    const page = parseInt(url.searchParams.get('page') || '1')
    const limit = parseInt(url.searchParams.get('limit') || '50')
    const offset = (page - 1) * limit

    // Validate pagination parameters
    if (page < 1 || limit < 1 || limit > 1000) {
      return new Response(
        JSON.stringify({ error: 'Invalid pagination parameters. Page must be >= 1, limit must be 1-1000' }),
        { status: 400, headers: { ...corsHeaders, 'Content-Type': 'application/json' } }
      )
    }

    // Determine user scope: admins see all users, regular users see only their own
    const userIdFilter = isAdmin ? null : user.id

    // Query deployments with unified function
    const { data: deployments, error } = await supabase.rpc('query_deployment_operations', {
      p_user_id: userIdFilter,
      p_resource_name: resourceName || null,
      p_platform: platform || null,
      p_status: status || null,
      p_operation_type: operationType || null,
      p_limit: limit,
      p_offset: offset
    })

    if (error) {
      console.error('Database error:', error)
      return new Response(
        JSON.stringify({ error: 'Failed to fetch deployment data' }),
        { status: 500, headers: { ...corsHeaders, 'Content-Type': 'application/json' } }
      )
    }

    // Get total count with same filters
    const { data: count, error: countError } = await supabase.rpc('count_deployment_operations', {
      p_user_id: userIdFilter,
      p_resource_name: resourceName || null,
      p_platform: platform || null,
      p_status: status || null,
      p_operation_type: operationType || null
    })

    if (countError) {
      console.error('Count error:', countError)
      return new Response(
        JSON.stringify({ error: 'Failed to fetch deployment count' }),
        { status: 500, headers: { ...corsHeaders, 'Content-Type': 'application/json' } }
      )
    }

    const response = {
      data: deployments || [],
      pagination: {
        page,
        limit,
        total: count || 0,
        total_pages: Math.ceil((count || 0) / limit)
      }
    }

    return new Response(
      JSON.stringify(response),
      { status: 200, headers: { ...corsHeaders, 'Content-Type': 'application/json' } }
    )

  } catch (error) {
    console.error('Deployment fetch error:', error)
    return new Response(
      JSON.stringify({ error: 'Internal server error' }),
      { status: 500, headers: { ...corsHeaders, 'Content-Type': 'application/json' } }
    )
  }
}

async function handlePostDeployments(req: Request, supabase: any) {
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
    const forwarded_for = req.headers.get('x-forwarded-for') || req.headers.get('x-real-ip') || 'unknown'
    const ip_address = forwarded_for.split(',')[0].trim()
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
}

Deno.serve(async (req) => {
  
  // Handle CORS preflight requests
  if (req.method === 'OPTIONS') {
    return new Response('ok', { headers: corsHeaders })
  }

  if (req.method !== 'POST' && req.method !== 'GET') {
    return new Response(
      JSON.stringify({ error: 'Method not allowed' }),
      { status: 405, headers: { ...corsHeaders, 'Content-Type': 'application/json' } }
    )
  }

  // Extract access token from custom CLI token or use Authorization header directly
  let accessToken = req.headers.get('authorization')?.replace('Bearer ', '')
  
  // If the token looks like a custom CLI token (base64 JSON), extract the access_token
  if (accessToken && !accessToken.includes('.')) {
    try {
      const tokenData = JSON.parse(atob(accessToken))
      if (tokenData.access_token) {
        accessToken = tokenData.access_token
        console.log('Extracted access token from custom CLI token')
      }
    } catch (error) {
      console.log('Token is not a custom CLI token, using as-is')
    }
  }

  const supabase = createClient(
    Deno.env.get('SUPABASE_URL')!,
    Deno.env.get('SUPABASE_SERVICE_ROLE_KEY')!,
    {
      global: {
        headers: {
          Authorization: accessToken ? `Bearer ${accessToken}` : undefined
        }
      }
    }
  )

  if (req.method === 'GET') {
    return handleGetDeployments(req, supabase, accessToken)
  }

  if (req.method === 'POST') {
    return handlePostDeployments(req, supabase)
  }

  // Should never reach here due to method check above, but for type safety
  return new Response(
    JSON.stringify({ error: 'Method not allowed' }),
    { status: 405, headers: { ...corsHeaders, 'Content-Type': 'application/json' } }
  )
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
