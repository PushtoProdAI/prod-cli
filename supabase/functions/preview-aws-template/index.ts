import 'jsr:@supabase/functions-js/edge-runtime.d.ts';
import { createClient } from 'jsr:@supabase/supabase-js@2';
import { initSentry, captureException, flushSentry } from '../_shared/sentry.ts';

// Import types and helper functions from deploy-aws-stack
import type { DeploymentSpec } from '../deploy-aws-stack/types.ts';
import { generateCloudFormationTemplate } from '../deploy-aws-stack/template-generator.ts';

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

    if (!data.user) {
      return new Response("Unauthorized", { status: 401 });
    }

    const deploymentSpec: DeploymentSpec = await req.json();

    console.log('Received template preview request:', {
      serviceName: deploymentSpec.serviceName,
      backingServicesCount: deploymentSpec.backingServices?.length || 0,
      envVarsCount: deploymentSpec.envVars?.length || 0,
    });

    // Validate required fields
    if (!deploymentSpec.serviceName) {
      return new Response("Missing required field: serviceName", { status: 400 });
    }

    // Get customer AWS region from database for accurate template generation
    const { data: awsCredentials } = await supabaseClient
      .from('aws_credentials')
      .select('region')
      .eq('user_id', data.user.id)
      .single();

    const awsRegion = awsCredentials?.region || 'us-east-1';

    // Generate CloudFormation template
    // Use a placeholder image URL since we're just generating the template for pricing
    const previewSpec = {
      ...deploymentSpec,
      imageUrl: deploymentSpec.imageUrl || 'placeholder.dkr.ecr.us-east-1.amazonaws.com/app:latest',
    };

    const template = generateCloudFormationTemplate(previewSpec, data.user.id, awsRegion);

    // Return the template
    return Response.json({
      template: template,
      serviceName: deploymentSpec.serviceName,
    });

  } catch (error) {
    console.error('Error in preview-aws-template function:', error);
    captureException(error instanceof Error ? error : new Error(String(error)), {
      function: 'preview-aws-template',
      operation: 'generate_template',
      method: req.method
    });
    await flushSentry();

    return new Response(
      JSON.stringify({ error: error instanceof Error ? error.message : 'Internal server error' }),
      { status: 500, headers: { 'Content-Type': 'application/json' } }
    );
  }
});