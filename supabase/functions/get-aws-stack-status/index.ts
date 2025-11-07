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
  DescribeStackResourcesCommand,
  StackStatus,
} from "npm:@aws-sdk/client-cloudformation";

// Initialize Sentry
initSentry();

interface StackStatusRequest {
  stackName: string;
  includeResources?: boolean; // If true, fetch and return resource details
}

interface StackResources {
  hasRDS: boolean;
  hasElastiCache: boolean;
  hasAppRunner: boolean;
  rdsInstances: string[];
  elastiCacheInstances: string[];
}

interface StackStatusResponse {
  exists: boolean; // Added to support detection use case
  stackId: string;
  stackName: string;
  status: string;
  outputs?: Record<string, string>;
  resources?: StackResources; // Added to support detection use case
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
    let stack;
    try {
      const describeResult = await cfnClient.send(
        new DescribeStacksCommand({ StackName: statusRequest.stackName })
      );
      stack = describeResult.Stacks?.[0];
    } catch (error: any) {
      // Stack doesn't exist
      if (error.name === 'ValidationError' || error.message?.includes('does not exist')) {
        console.log('Stack does not exist:', statusRequest.stackName);
        return Response.json({
          exists: false,
          stackId: '',
          stackName: statusRequest.stackName,
          status: 'NOT_FOUND',
        });
      }
      // Other errors should be thrown
      throw error;
    }

    if (!stack) {
      return Response.json({
        exists: false,
        stackId: '',
        stackName: statusRequest.stackName,
        status: 'NOT_FOUND',
      });
    }

    const status = stack.StackStatus;
    const response: StackStatusResponse = {
      exists: true,
      stackId: stack.StackId || statusRequest.stackName,
      stackName: statusRequest.stackName,
      status: status,
    };

    // Extract outputs (always include if available)
    const outputs: Record<string, string> = {};
    if (stack.Outputs) {
      for (const output of stack.Outputs) {
        if (output.OutputKey && output.OutputValue) {
          outputs[output.OutputKey] = output.OutputValue;
        }
      }
      response.outputs = outputs;
    }

    // Get stack resources if requested (for detection use case)
    if (statusRequest.includeResources) {
      try {
        const resourcesResult = await cfnClient.send(
          new DescribeStackResourcesCommand({ StackName: statusRequest.stackName })
        );

        const resources: StackResources = {
          hasRDS: false,
          hasElastiCache: false,
          hasAppRunner: false,
          rdsInstances: [],
          elastiCacheInstances: [],
        };

        if (resourcesResult.StackResources) {
          for (const resource of resourcesResult.StackResources) {
            const resourceType = resource.ResourceType;
            const logicalId = resource.LogicalResourceId || '';
            
            if (resourceType === 'AWS::RDS::DBInstance') {
              resources.hasRDS = true;
              resources.rdsInstances.push(logicalId);
              console.log('Found RDS instance:', logicalId);
            } else if (resourceType === 'AWS::ElastiCache::CacheCluster') {
              resources.hasElastiCache = true;
              resources.elastiCacheInstances.push(logicalId);
              console.log('Found ElastiCache cluster:', logicalId);
            } else if (resourceType === 'AWS::AppRunner::Service') {
              resources.hasAppRunner = true;
              console.log('Found App Runner service:', logicalId);
            }
          }
        }

        response.resources = resources;
        console.log('Stack resources:', resources);

      } catch (error: any) {
        console.error('Failed to describe stack resources:', error.message);
        // Don't fail the entire request if we can't get resources
        // Just return without resources field
      }
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
