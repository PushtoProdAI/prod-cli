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
  DescribeStackResourcesCommand,
  StackStatus,
} from "npm:@aws-sdk/client-cloudformation";

// Initialize Sentry
initSentry();

interface CheckStackRequest {
  stackName: string;
}

interface StackResources {
  hasRDS: boolean;
  hasElastiCache: boolean;
  hasAppRunner: boolean;
  rdsInstances: string[];
  elastiCacheInstances: string[];
}

interface CheckStackResponse {
  exists: boolean;
  stackId?: string;
  status?: string;
  outputs?: Record<string, string>;
  resources?: StackResources;
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

    const checkRequest: CheckStackRequest = await req.json();

    // Validate required fields
    if (!checkRequest.stackName) {
      return new Response("Missing required field: stackName", { status: 400 });
    }

    console.log('Checking for existing AWS stack:', checkRequest.stackName);

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
      RoleSessionName: `check-${data.user.id}`,
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

    // Check if stack exists
    let stack;
    try {
      const describeResult = await cfnClient.send(
        new DescribeStacksCommand({ StackName: checkRequest.stackName })
      );
      stack = describeResult.Stacks?.[0];
    } catch (error: any) {
      // Stack doesn't exist - this is not an error, just means first deploy
      if (error.name === 'ValidationError' || error.message?.includes('does not exist')) {
        console.log('Stack does not exist:', checkRequest.stackName);
        return Response.json({
          exists: false,
        });
      }
      // Other errors should be thrown
      throw error;
    }

    if (!stack) {
      return Response.json({
        exists: false,
      });
    }

    console.log('Stack found:', stack.StackId, 'Status:', stack.StackStatus);

    // Stack exists - build response
    const response: CheckStackResponse = {
      exists: true,
      stackId: stack.StackId || checkRequest.stackName,
      status: stack.StackStatus,
    };

    // Extract outputs
    const outputs: Record<string, string> = {};
    if (stack.Outputs) {
      for (const output of stack.Outputs) {
        if (output.OutputKey && output.OutputValue) {
          outputs[output.OutputKey] = output.OutputValue;
        }
      }
      response.outputs = outputs;
    }

    // Get stack resources to detect what infrastructure exists
    try {
      const resourcesResult = await cfnClient.send(
        new DescribeStackResourcesCommand({ StackName: checkRequest.stackName })
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
      // Just return what we have
      response.resources = {
        hasRDS: false,
        hasElastiCache: false,
        hasAppRunner: false,
        rdsInstances: [],
        elastiCacheInstances: [],
      };
    }

    return Response.json(response);

  } catch (error) {
    console.error('Unexpected error in check-aws-stack function:', error);
    captureException(error instanceof Error ? error : new Error(String(error)), {
      function: 'check-aws-stack',
      operation: 'general_error',
      method: req.method
    });
    await flushSentry();

    return new Response(
      JSON.stringify({ 
        exists: false,
        error: error instanceof Error ? error.message : 'Internal server error' 
      }),
      { status: 500, headers: { 'Content-Type': 'application/json' } }
    );
  }
});
