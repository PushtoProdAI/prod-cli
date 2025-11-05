import 'jsr:@supabase/functions-js/edge-runtime.d.ts';
import { createClient } from 'jsr:@supabase/supabase-js@2';
import { initSentry, captureException, flushSentry } from '../_shared/sentry.ts';
import {
  STSClient,
  AssumeRoleCommand,
} from "npm:@aws-sdk/client-sts";
import {
  AppRunnerClient,
  DescribeServiceCommand,
  ServiceStatus,
} from "npm:@aws-sdk/client-apprunner";

// Initialize Sentry
initSentry();

interface AppRunnerStatusRequest {
  serviceArn: string;
}

interface AppRunnerStatusResponse {
  status: string;
  error?: string;
}

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

    const statusRequest: AppRunnerStatusRequest = await req.json();

    // Validate required fields
    if (!statusRequest.serviceArn) {
      return new Response("Missing required field: serviceArn", { status: 400 });
    }

    // Get customer AWS credentials from database
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

    const awsRegion = awsCredentials.region || 'us-east-1';

    // Assume customer's AWS role
    const stsClient = new STSClient({
      region: awsRegion,
      credentials: {
        accessKeyId: Deno.env.get('AWS_ACCESS_KEY_ID') || '',
        secretAccessKey: Deno.env.get('AWS_SECRET_ACCESS_KEY') || '',
      },
    });

    const assumeRoleParams: any = {
      RoleArn: awsCredentials.role_arn,
      RoleSessionName: `apprunner-status-${data.user.id}`,
      DurationSeconds: 3600,
    };

    if (awsCredentials.external_id) {
      assumeRoleParams.ExternalId = awsCredentials.external_id;
    }

    const assumeRoleCommand = new AssumeRoleCommand(assumeRoleParams);
    const { Credentials } = await stsClient.send(assumeRoleCommand);

    if (!Credentials) {
      return new Response('Failed to assume AWS role', { status: 500 });
    }

    // Create App Runner client with assumed credentials
    const appRunnerClient = new AppRunnerClient({
      region: awsRegion,
      credentials: {
        accessKeyId: Credentials.AccessKeyId!,
        secretAccessKey: Credentials.SecretAccessKey!,
        sessionToken: Credentials.SessionToken,
      },
    });

    // Get service status
    const describeResult = await appRunnerClient.send(
      new DescribeServiceCommand({ ServiceArn: statusRequest.serviceArn })
    );

    const service = describeResult.Service;
    if (!service) {
      return new Response(
        JSON.stringify({ error: 'App Runner service not found' }),
        { status: 404, headers: { 'Content-Type': 'application/json' } }
      );
    }

    const status = service.Status;
    const response: AppRunnerStatusResponse = {
      status: status || 'UNKNOWN',
    };

    // Check for failure states
    if (
      status === ServiceStatus.CREATE_FAILED ||
      status === ServiceStatus.OPERATION_FAILED ||
      status === ServiceStatus.DELETE_FAILED
    ) {
      // Try to get a meaningful error message
      response.error = 'App Runner service operation failed';
    }

    return Response.json(response);

  } catch (error) {
    console.error('Unexpected error in get-apprunner-status function:', error);
    captureException(error instanceof Error ? error : new Error(String(error)), {
      function: 'get-apprunner-status',
      operation: 'general_error',
      method: req.method
    });
    await flushSentry();

    return new Response(
      JSON.stringify({ error: error instanceof Error ? error.message : 'Internal server error' }),
      { status: 500, headers: { 'Content-Type': 'application/json' } }
    );
  }
});
