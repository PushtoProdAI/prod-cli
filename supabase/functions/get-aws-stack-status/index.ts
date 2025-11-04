import 'jsr:@supabase/functions-js/edge-runtime.d.ts';
import { createClient } from 'jsr:@supabase/supabase-js@2';
import { initSentry, captureException, flushSentry } from '../_shared/sentry.ts';
import {
  STSClient,
  AssumeRoleCommand,
} from "npm:@aws-sdk/client-sts";
import {
  CloudFormationClient,
  DescribeStacksCommand,
  DescribeStackEventsCommand,
  StackStatus,
} from "npm:@aws-sdk/client-cloudformation";

// Initialize Sentry
initSentry();

interface StackStatusRequest {
  stackName: string;
}

interface StackStatusResponse {
  stackId: string;
  stackName: string;
  status: string;
  outputs?: Record<string, string>;
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

    const statusRequest: StackStatusRequest = await req.json();

    // Validate required fields
    if (!statusRequest.stackName) {
      return new Response("Missing required field: stackName", { status: 400 });
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
      RoleSessionName: `status-${data.user.id}`,
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

    // Create CloudFormation client with assumed credentials
    const cfnClient = new CloudFormationClient({
      region: awsRegion,
      credentials: {
        accessKeyId: Credentials.AccessKeyId!,
        secretAccessKey: Credentials.SecretAccessKey!,
        sessionToken: Credentials.SessionToken,
      },
    });

    // Get stack status
    const describeResult = await cfnClient.send(
      new DescribeStacksCommand({ StackName: statusRequest.stackName })
    );

    const stack = describeResult.Stacks?.[0];
    if (!stack) {
      return new Response(
        JSON.stringify({ error: 'Stack not found' }),
        { status: 404, headers: { 'Content-Type': 'application/json' } }
      );
    }

    const status = stack.StackStatus;
    const response: StackStatusResponse = {
      stackId: stack.StackId || statusRequest.stackName,
      stackName: statusRequest.stackName,
      status: status,
    };

    // Check if stack is in a terminal state
    if (
      status === StackStatus.CREATE_COMPLETE ||
      status === StackStatus.UPDATE_COMPLETE
    ) {
      // Extract outputs
      const outputs: Record<string, string> = {};
      if (stack.Outputs) {
        for (const output of stack.Outputs) {
          if (output.OutputKey && output.OutputValue) {
            outputs[output.OutputKey] = output.OutputValue;
          }
        }
      }
      response.outputs = outputs;
    }

    // Check for failure states
    if (
      status === StackStatus.CREATE_FAILED ||
      status === StackStatus.ROLLBACK_COMPLETE ||
      status === StackStatus.ROLLBACK_FAILED ||
      status === StackStatus.UPDATE_ROLLBACK_COMPLETE ||
      status === StackStatus.UPDATE_ROLLBACK_FAILED
    ) {
      // Get stack events to find the error
      try {
        const eventsResult = await cfnClient.send(
          new DescribeStackEventsCommand({ StackName: statusRequest.stackName })
        );

        const failedEvent = eventsResult.StackEvents?.find(
          e => e.ResourceStatus?.includes('FAILED')
        );

        response.error = failedEvent?.ResourceStatusReason || 'Stack operation failed';
      } catch (err) {
        // If we can't get events, just set a generic error
        response.error = 'Stack operation failed';
      }
    }

    return Response.json(response);

  } catch (error) {
    console.error('Unexpected error in get-aws-stack-status function:', error);
    captureException(error instanceof Error ? error : new Error(String(error)), {
      function: 'get-aws-stack-status',
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
