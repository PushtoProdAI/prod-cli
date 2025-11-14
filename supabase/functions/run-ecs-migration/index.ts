import 'jsr:@supabase/functions-js/edge-runtime.d.ts';
import { createClient } from 'jsr:@supabase/supabase-js@2';
import { initSentry, captureException, flushSentry } from '../_shared/sentry.ts';
import {
  STSClient,
  AssumeRoleCommand,
} from "npm:@aws-sdk/client-sts";
import {
  IAMClient,
  CreateServiceLinkedRoleCommand,
} from "npm:@aws-sdk/client-iam";
import {
  ECSClient,
  RunTaskCommand,
  DescribeTasksCommand,
  Task,
} from "npm:@aws-sdk/client-ecs";
import {
  CloudWatchLogsClient,
  GetLogEventsCommand,
  FilterLogEventsCommand,
} from "npm:@aws-sdk/client-cloudwatch-logs";

// Initialize Sentry
initSentry();

interface RunMigrationRequest {
  stackName: string;
  clusterArn: string;
  taskDefinitionArn: string;
  migrationCommand: string;
  subnets: string[];
  securityGroups: string[];
}

interface RunMigrationResponse {
  taskArn: string;
  status: string;
  success: boolean;
  exitCode?: number;
  logs?: string[];
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

    const migrationRequest: RunMigrationRequest = await req.json();

    // Validate required fields
    if (!migrationRequest.stackName || !migrationRequest.clusterArn || 
        !migrationRequest.taskDefinitionArn || !migrationRequest.migrationCommand) {
      return new Response("Missing required fields", { status: 400 });
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
      RoleSessionName: `migration-${data.user.id}`,
      DurationSeconds: 3600,
    };

    if (awsCredentials.external_id) {
      assumeRoleParams.ExternalId = awsCredentials.external_id;
    }

    const assumeRoleCommand = new AssumeRoleCommand(assumeRoleParams);
    const assumeRoleResponse = await stsClient.send(assumeRoleCommand);

    if (!assumeRoleResponse.Credentials) {
      throw new Error('Failed to assume AWS role');
    }

    // Ensure ECS service-linked role exists (required for Fargate networking)
    const iamClient = new IAMClient({
      region: awsRegion,
      credentials: {
        accessKeyId: assumeRoleResponse.Credentials.AccessKeyId!,
        secretAccessKey: assumeRoleResponse.Credentials.SecretAccessKey!,
        sessionToken: assumeRoleResponse.Credentials.SessionToken!,
      },
    });

    try {
      await iamClient.send(new CreateServiceLinkedRoleCommand({
        AWSServiceName: 'ecs.amazonaws.com',
      }));
      // Wait for IAM propagation
      await new Promise(resolve => setTimeout(resolve, 5000));
    } catch (error: any) {
      // If role already exists, that's fine - continue silently
      if (error.name !== 'InvalidInputException' || !error.message?.includes('has been taken')) {
        console.error('Warning creating service-linked role:', error.message);
      }
    }

    // Create ECS client with assumed role credentials
    const ecsClient = new ECSClient({
      region: awsRegion,
      credentials: {
        accessKeyId: assumeRoleResponse.Credentials.AccessKeyId!,
        secretAccessKey: assumeRoleResponse.Credentials.SecretAccessKey!,
        sessionToken: assumeRoleResponse.Credentials.SessionToken!,
      },
    });

    // Run the ECS task with migration command override
    const runTaskCommand = new RunTaskCommand({
      cluster: migrationRequest.clusterArn,
      taskDefinition: migrationRequest.taskDefinitionArn,
      launchType: 'FARGATE',
      networkConfiguration: {
        awsvpcConfiguration: {
          subnets: migrationRequest.subnets,
          securityGroups: migrationRequest.securityGroups,
          assignPublicIp: 'ENABLED', // Public subnets needed for ECR access
        },
      },
      overrides: {
        containerOverrides: [
          {
            name: 'migration',
            command: ['sh', '-c', migrationRequest.migrationCommand],
          },
        ],
      },
    });

    const runTaskResponse = await ecsClient.send(runTaskCommand);

    // Check for failures in the response
    if (runTaskResponse.failures && runTaskResponse.failures.length > 0) {
      const failure = runTaskResponse.failures[0];
      console.error('ECS RunTask failure:', failure);
      throw new Error(`ECS RunTask failed: ${failure.reason || 'Unknown reason'}. Detail: ${failure.detail || 'No details'}`);
    }

    if (!runTaskResponse.tasks || runTaskResponse.tasks.length === 0) {
      throw new Error('Failed to start migration task - no tasks returned and no failures reported');
    }

    const task = runTaskResponse.tasks[0];
    const taskArn = task.taskArn!;

    // Poll for task completion
    let taskStatus = 'PENDING';
    let attempts = 0;
    const maxAttempts = 60; // 10 minutes max (10 second intervals)

    while (attempts < maxAttempts && taskStatus !== 'STOPPED') {
      await new Promise(resolve => setTimeout(resolve, 10000)); // Wait 10 seconds

      const describeCommand = new DescribeTasksCommand({
        cluster: migrationRequest.clusterArn,
        tasks: [taskArn],
      });

      const describeResponse = await ecsClient.send(describeCommand);
      
      if (describeResponse.tasks && describeResponse.tasks.length > 0) {
        const currentTask = describeResponse.tasks[0];
        taskStatus = currentTask.lastStatus || 'UNKNOWN';

        if (taskStatus === 'STOPPED') {
          // Get exit code
          const container = currentTask.containers?.[0];
          const exitCode = container?.exitCode;

          // Fetch logs from CloudWatch
          const logs = await fetchTaskLogs(
            awsRegion,
            assumeRoleResponse.Credentials,
            migrationRequest.stackName,
            taskArn
          );

          const response: RunMigrationResponse = {
            taskArn,
            status: exitCode === 0 ? 'success' : 'failed',
            success: exitCode === 0,
            exitCode,
            logs,
          };

          if (exitCode !== 0) {
            response.error = currentTask.stoppedReason || 'Migration task failed';
          }

          return new Response(JSON.stringify(response), {
            status: exitCode === 0 ? 200 : 500,
            headers: { 'Content-Type': 'application/json' },
          });
        }
      }

      attempts++;
    }

    // Timeout
    return new Response(JSON.stringify({
      error: 'Migration task timeout',
      taskArn,
      status: 'timeout',
    }), {
      status: 408,
      headers: { 'Content-Type': 'application/json' },
    });

  } catch (error) {
    console.error('Error running migration:', error);
    captureException(error);
    await flushSentry();
    
    return new Response(JSON.stringify({ 
      error: error instanceof Error ? error.message : 'Unknown error',
    }), {
      status: 500,
      headers: { 'Content-Type': 'application/json' },
    });
  }
});

async function fetchTaskLogs(
  region: string,
  credentials: any,
  stackName: string,
  taskArn: string
): Promise<string[]> {
  try {
    const logsClient = new CloudWatchLogsClient({
      region,
      credentials: {
        accessKeyId: credentials.AccessKeyId!,
        secretAccessKey: credentials.SecretAccessKey!,
        sessionToken: credentials.SessionToken!,
      },
    });

    // Extract task ID from ARN
    const taskId = taskArn.split('/').pop();
    
    // Stack name format is 'prod-{serviceName}', extract serviceName
    // Log group name format is '/ecs/prod-{serviceName}'
    const serviceName = stackName.replace(/^prod-/, '');
    const logGroupName = `/ecs/prod-${serviceName}`;

    const filterCommand = new FilterLogEventsCommand({
      logGroupName,
      logStreamNamePrefix: `migration/${taskId}`,
      limit: 100,
    });

    const response = await logsClient.send(filterCommand);
    
    if (response.events) {
      return response.events.map(event => event.message || '').filter(msg => msg.length > 0);
    }

    return [];
  } catch (error) {
    console.error('Error fetching logs:', error);
    return [`Error fetching logs: ${error instanceof Error ? error.message : 'Unknown error'}`];
  }
}
