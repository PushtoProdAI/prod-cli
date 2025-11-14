import "jsr:@supabase/functions-js/edge-runtime.d.ts"
import { createClient } from "https://esm.sh/@supabase/supabase-js@2"
import { initSentry, captureException, flushSentry } from '../_shared/sentry.ts'
import {
  STSClient,
  AssumeRoleCommand,
} from "npm:@aws-sdk/client-sts"
import {
  IAMClient,
  GetRoleCommand,
} from "npm:@aws-sdk/client-iam"

initSentry()

const corsHeaders = {
  'Access-Control-Allow-Origin': '*',
  'Access-Control-Allow-Headers': 'authorization, x-client-info, apikey, content-type',
  'Access-Control-Allow-Methods': 'GET, POST, OPTIONS',
  'Access-Control-Max-Age': '86400',
}

Deno.serve(async (req) => {
  try {
    if (req.method === 'OPTIONS') {
      return new Response('ok', { headers: corsHeaders })
    }

    const supabase = createClient(
      Deno.env.get("SUPABASE_URL")!,
      Deno.env.get("SUPABASE_ANON_KEY")!,
      {
        global: {
          headers: { Authorization: req.headers.get('Authorization')! },
        },
      }
    )

    const { data: { user }, error: authError } = await supabase.auth.getUser()

    if (authError || !user) {
      return new Response(
        JSON.stringify({ error: 'Unauthorized' }),
        { status: 401, headers: { ...corsHeaders, 'Content-Type': 'application/json' } }
      )
    }

    // GET - Check if user has AWS credentials configured
    if (req.method === 'GET') {
      try {
        const { data, error } = await supabase.rpc('check_aws_authentication')

        if (error) {
          console.error('Error checking AWS authentication:', error)
          captureException(new Error(String(error)), {
            function: 'aws-auth',
            operation: 'check',
            user_id: user.id
          })
          return new Response(
            JSON.stringify({ error: 'Failed to check AWS authentication' }),
            { status: 500, headers: { ...corsHeaders, 'Content-Type': 'application/json' } }
          )
        }

        return new Response(
          JSON.stringify({ authenticated: data }),
          { status: 200, headers: { ...corsHeaders, 'Content-Type': 'application/json' } }
        )
      } catch (error) {
        console.error('Error in GET /aws-auth:', error)
        captureException(error instanceof Error ? error : new Error(String(error)), {
          function: 'aws-auth',
          operation: 'check_error',
          user_id: user.id
        })
        return new Response(
          JSON.stringify({ error: 'Internal server error' }),
          { status: 500, headers: { ...corsHeaders, 'Content-Type': 'application/json' } }
        )
      }
    }

    // POST - Initialize AWS auth setup or store credentials
    if (req.method === 'POST') {
      try {
        const body = await req.json()
        const action = body.action

        // Initialize AWS auth setup - generate external ID
        if (action === 'initialize') {
          // Generate a unique external ID
          const externalId = `prod-${user.id.substring(0, 8)}-${crypto.randomUUID().substring(0, 8)}`
          
          // Store the external ID (role_arn will be added when user completes setup)
          const { error: insertError } = await supabase
            .from('aws_credentials')
            .upsert({
              user_id: user.id,
              external_id: externalId,
              role_arn: null,  // Will be set when user completes CloudFormation setup
              region: body.region || 'us-east-1'
            }, {
              onConflict: 'user_id'
            })

          if (insertError) {
            console.error('Error storing external ID:', insertError)
            captureException(new Error(String(insertError)), {
              function: 'aws-auth',
              operation: 'initialize',
              user_id: user.id
            })
            return new Response(
              JSON.stringify({ error: 'Failed to initialize AWS auth setup' }),
              { status: 500, headers: { ...corsHeaders, 'Content-Type': 'application/json' } }
            )
          }

          return new Response(
            JSON.stringify({ 
              external_id: externalId,
              region: body.region || 'us-east-1'
            }),
            { status: 200, headers: { ...corsHeaders, 'Content-Type': 'application/json' } }
          )
        }

        // Complete AWS auth setup - store role ARN
        if (action === 'complete') {
          const { role_arn, region } = body

          if (!role_arn || typeof role_arn !== 'string') {
            return new Response(
              JSON.stringify({ error: 'role_arn is required' }),
              { status: 400, headers: { ...corsHeaders, 'Content-Type': 'application/json' } }
            )
          }

          // Validate role ARN format
          const roleArnPattern = /^arn:aws:iam::[0-9]{12}:role\/[a-zA-Z0-9+=,.@_-]+$/
          if (!roleArnPattern.test(role_arn)) {
            return new Response(
              JSON.stringify({ error: 'Invalid role ARN format' }),
              { status: 400, headers: { ...corsHeaders, 'Content-Type': 'application/json' } }
            )
          }

          // Get the existing record to ensure external_id exists
          const { data: existing, error: fetchError } = await supabase
            .from('aws_credentials')
            .select('external_id')
            .eq('user_id', user.id)
            .single()

          if (fetchError || !existing) {
            return new Response(
              JSON.stringify({ error: 'AWS auth setup not initialized. Please start the setup process again.' }),
              { status: 400, headers: { ...corsHeaders, 'Content-Type': 'application/json' } }
            )
          }

          // SECURITY: Verify the role has required tags before storing
          // This ensures only roles created from our CloudFormation template can be used
          try {
            // Extract account ID and role name from ARN
            const arnParts = role_arn.match(/^arn:aws:iam::([0-9]{12}):role\/(.+)$/)
            if (!arnParts) {
              return new Response(
                JSON.stringify({ error: 'Invalid role ARN format' }),
                { status: 400, headers: { ...corsHeaders, 'Content-Type': 'application/json' } }
              )
            }
            const [, accountId, roleName] = arnParts

            // Create IAM client to check role tags
            // We use our backend credentials to call GetRole on the customer's account
            const iamClient = new IAMClient({
              region: region || 'us-east-1',
              credentials: {
                accessKeyId: Deno.env.get('AWS_ACCESS_KEY_ID') || '',
                secretAccessKey: Deno.env.get('AWS_SECRET_ACCESS_KEY') || '',
              },
            })

            // First, we need to assume a role that has permission to call iam:GetRole
            // But we have a chicken-and-egg problem: we can't assume the role until we verify it
            // Solution: We'll attempt to assume it with the external_id, and if that succeeds,
            // then check the tags. If assume fails, the role isn't properly configured anyway.
            const stsClient = new STSClient({
              region: region || 'us-east-1',
              credentials: {
                accessKeyId: Deno.env.get('AWS_ACCESS_KEY_ID') || '',
                secretAccessKey: Deno.env.get('AWS_SECRET_ACCESS_KEY') || '',
              },
            })

            // Try to assume the role to verify external_id and trust relationship
            const assumeRoleParams = {
              RoleArn: role_arn,
              RoleSessionName: `validation-${user.id}`,
              DurationSeconds: 900, // 15 minutes, just for validation
              ExternalId: existing.external_id,
            }

            let credentials
            try {
              const assumeResult = await stsClient.send(new AssumeRoleCommand(assumeRoleParams))
              credentials = assumeResult.Credentials
            } catch (assumeError: any) {
              console.error('Failed to assume role during validation:', assumeError)
              return new Response(
                JSON.stringify({ 
                  error: `Unable to assume role. Please verify: 1) The role exists, 2) Trust policy allows account ${Deno.env.get('PROD_AWS_ACCOUNT_ID') || '588738592923'}, 3) External ID matches, 4) Role has been created successfully in CloudFormation`
                }),
                { status: 400, headers: { ...corsHeaders, 'Content-Type': 'application/json' } }
              )
            }

            if (!credentials) {
              return new Response(
                JSON.stringify({ error: 'Failed to validate role credentials' }),
                { status: 500, headers: { ...corsHeaders, 'Content-Type': 'application/json' } }
              )
            }

            // Now use the assumed role credentials to check tags
            const iamClientAssumed = new IAMClient({
              region: region || 'us-east-1',
              credentials: {
                accessKeyId: credentials.AccessKeyId!,
                secretAccessKey: credentials.SecretAccessKey!,
                sessionToken: credentials.SessionToken,
              },
            })

            const getRoleResult = await iamClientAssumed.send(
              new GetRoleCommand({ RoleName: roleName })
            )

            // Check for required tags
            const tags = getRoleResult.Role?.Tags || []
            const managedByTag = tags.find(t => t.Key === 'ManagedBy')
            const purposeTag = tags.find(t => t.Key === 'Purpose')

            if (managedByTag?.Value !== 'Prod' || purposeTag?.Value !== 'Deployment') {
              console.warn('Role missing required tags:', { role_arn, tags })
              return new Response(
                JSON.stringify({ 
                  error: 'Role is missing required tags. Please use the official CloudFormation template from https://prod-aws-deploy.s3.amazonaws.com/cloudformation-deploy-role-template.yaml'
                }),
                { status: 400, headers: { ...corsHeaders, 'Content-Type': 'application/json' } }
              )
            }

            console.log('✓ Role validation successful:', { role_arn, tags: { managedByTag, purposeTag } })

          } catch (validationError: any) {
            console.error('Error validating role:', validationError)
            captureException(validationError instanceof Error ? validationError : new Error(String(validationError)), {
              function: 'aws-auth',
              operation: 'validate_role',
              user_id: user.id,
              role_arn: role_arn
            })
            return new Response(
              JSON.stringify({ 
                error: `Failed to validate role: ${validationError.message || 'Unknown error'}. Please ensure you used the official CloudFormation template.`
              }),
              { status: 400, headers: { ...corsHeaders, 'Content-Type': 'application/json' } }
            )
          }

          // Update with the actual role ARN
          const { error: updateError } = await supabase
            .from('aws_credentials')
            .update({
              role_arn: role_arn,
              region: region || 'us-east-1'
            })
            .eq('user_id', user.id)

          if (updateError) {
            console.error('Error storing role ARN:', updateError)
            captureException(new Error(String(updateError)), {
              function: 'aws-auth',
              operation: 'complete',
              user_id: user.id
            })
            return new Response(
              JSON.stringify({ error: 'Failed to store AWS credentials' }),
              { status: 500, headers: { ...corsHeaders, 'Content-Type': 'application/json' } }
            )
          }

          return new Response(
            JSON.stringify({ success: true }),
            { status: 200, headers: { ...corsHeaders, 'Content-Type': 'application/json' } }
          )
        }

        return new Response(
          JSON.stringify({ error: 'Invalid action. Use "initialize" or "complete"' }),
          { status: 400, headers: { ...corsHeaders, 'Content-Type': 'application/json' } }
        )
      } catch (error) {
        console.error('Error in POST /aws-auth:', error)
        captureException(error instanceof Error ? error : new Error(String(error)), {
          function: 'aws-auth',
          operation: 'post_error',
          user_id: user.id
        })
        return new Response(
          JSON.stringify({ error: 'Internal server error' }),
          { status: 500, headers: { ...corsHeaders, 'Content-Type': 'application/json' } }
        )
      }
    }

    return new Response(
      JSON.stringify({ error: 'Method not allowed' }),
      { status: 405, headers: { ...corsHeaders, 'Content-Type': 'application/json' } }
    )

  } catch (error) {
    console.error('Unexpected error in aws-auth function:', error)
    captureException(error instanceof Error ? error : new Error(String(error)), {
      function: 'aws-auth',
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
