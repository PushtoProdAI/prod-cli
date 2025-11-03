import 'jsr:@supabase/functions-js/edge-runtime.d.ts';
import { createClient } from 'jsr:@supabase/supabase-js@2';
import { checkAndCreateECRRepo } from '../_shared/aws.ts';
import { initSentry, captureException, flushSentry } from '../_shared/sentry.ts';

// Initialize Sentry
initSentry();

Deno.serve(async (req) => {
  try {
    if (req.method !== 'POST') {
      return new Response(JSON.stringify({ error: 'Method not allowed' }), {
        status: 405,
        headers: { 'Content-Type': 'application/json' },
      });
    }
  const supabaseClient = createClient(
    Deno.env.get('SUPABASE_URL') ?? '',
    Deno.env.get('SUPABASE_ANON_KEY') ?? '',
    {
      global: {
        headers: { Authorization: req.headers.get('Authorization')! },
      },
    },
  );

  const authHeader = req.headers.get('Authorization')!;
  const token = authHeader.replace('Bearer ', '');
  const { data } = await supabaseClient.auth.getUser(token);

  const { name, location } = await req.json();
  if (!name) {
    return new Response("Missing name", { status: 400 });
  }

  // Validate location parameter
  if (location && location !== 'internal' && location !== 'external') {
    return new Response("Invalid location parameter. Must be 'internal' or 'external'", { status: 400 });
  }

  // Default to 'internal' for backwards compatibility
  const deployLocation = location || 'internal';

  let roleArn: string;
  let region: string;
  let externalId: string | null = null;

  if (deployLocation === 'external') {
    // Get customer AWS credentials from database for external deployment
    const { data: awsCredentials, error: credError } = await supabaseClient
      .from('aws_credentials')
      .select('role_arn, region, external_id')
      .eq('user_id', data.user.id)
      .single();

    if (credError || !awsCredentials) {
      return new Response("AWS credentials not found. Please authenticate with AWS first.", { status: 404 });
    }

    if (!awsCredentials.role_arn) {
      return new Response("AWS role ARN not configured. Please complete AWS authentication.", { status: 400 });
    }

    roleArn = awsCredentials.role_arn;
    region = awsCredentials.region;
    externalId = awsCredentials.external_id;
  } else {
    // Use internal Prod AWS account for internal deployment (Render, etc.)
    roleArn = Deno.env.get('AWS_ECR_ROLE_ARN')!;
    region = Deno.env.get('AWS_REGION') || 'us-east-1';
    externalId = null;
  }

  const result = await checkAndCreateECRRepo(data.user.id, name, roleArn, region, externalId);

  if (result instanceof Error) {
    console.error("Create repo error:", result);
    captureException(result, {
      function: 'create-repo',
      operation: 'ecr_repo_creation',
      user_id: data?.user?.id,
      repo_name: name
    });
    return new Response(result.message, { status: 500 });
  }

  return Response.json(result);
  
  } catch (error) {
    console.error('Unexpected error in create-repo function:', error);
    captureException(error instanceof Error ? error : new Error(String(error)), {
      function: 'create-repo',
      operation: 'general_error',
      method: req.method
    });
    await flushSentry();
    
    return new Response(
      JSON.stringify({ error: 'Internal server error' }),
      { status: 500, headers: { 'Content-Type': 'application/json' } }
    );
  }
});
