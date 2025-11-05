import 'jsr:@supabase/functions-js/edge-runtime.d.ts';
import { createClient } from 'jsr:@supabase/supabase-js@2';
import { initSentry, captureException, flushSentry } from '../_shared/sentry.ts';
import {
  STSClient,
  AssumeRoleCommand,
} from "npm:@aws-sdk/client-sts";
import {
  AppRunnerClient,
  CreateServiceCommand,
  CreateVpcConnectorCommand,
} from "npm:@aws-sdk/client-apprunner";

// Initialize Sentry
initSentry();

interface EnvVar {
  Name: string;
  Value: any; // Can be string or CloudFormation intrinsic function
}

interface CreateAppRunnerRequest {
  serviceName: string;
  imageUrl: string;
  cpu: string;
  memory: string;
  port: number;
  envVars: EnvVar[];
  roleArns?: {
    accessRoleArn: string;
    instanceRoleArn: string;
  };
  vpcConfig?: {
    vpcId: string;
    subnets: string[];
    securityGroups: string[];
  };
}

interface CreateAppRunnerResponse {
  success: boolean;
  serviceArn: string;
  serviceUrl: string;
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

    const appRunnerRequest: CreateAppRunnerRequest = await req.json();

    console.log('Creating App Runner service:', {
      serviceName: appRunnerRequest.serviceName,
      imageUrl: appRunnerRequest.imageUrl,
      hasVpcConfig: !!appRunnerRequest.vpcConfig,
    });

    // Validate required fields
    if (!appRunnerRequest.serviceName || !appRunnerRequest.imageUrl) {
      return new Response("Missing required fields: serviceName and imageUrl are required", { status: 400 });
    }

    if (!appRunnerRequest.roleArns?.accessRoleArn || !appRunnerRequest.roleArns?.instanceRoleArn) {
      return new Response("Missing required fields: roleArns.accessRoleArn and roleArns.instanceRoleArn are required", { status: 400 });
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
      RoleSessionName: `apprunner-${data.user.id}`,
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

    // Create App Runner client with assumed role credentials
    const appRunnerClient = new AppRunnerClient({
      region: awsRegion,
      credentials: {
        accessKeyId: assumeRoleResponse.Credentials.AccessKeyId!,
        secretAccessKey: assumeRoleResponse.Credentials.SecretAccessKey!,
        sessionToken: assumeRoleResponse.Credentials.SessionToken!,
      },
    });

    let vpcConnectorArn: string | undefined;

    // Create VPC Connector if VPC config is provided
    if (appRunnerRequest.vpcConfig) {
      console.log('Creating VPC Connector for App Runner');
      const createVpcConnectorCommand = new CreateVpcConnectorCommand({
        VpcConnectorName: `${appRunnerRequest.serviceName}-connector`,
        Subnets: appRunnerRequest.vpcConfig.subnets,
        SecurityGroups: appRunnerRequest.vpcConfig.securityGroups,
        Tags: [
          { Key: 'service', Value: appRunnerRequest.serviceName },
          { Key: 'ManagedBy', Value: 'Prod' },
        ],
      });

      const vpcConnectorResponse = await appRunnerClient.send(createVpcConnectorCommand);
      vpcConnectorArn = vpcConnectorResponse.VpcConnector?.VpcConnectorArn;
      console.log('VPC Connector created:', vpcConnectorArn);
    }

    // Prepare environment variables - convert CloudFormation intrinsic functions to strings
    const runtimeEnvVars: Record<string, string> = {};
    for (const envVar of appRunnerRequest.envVars) {
      // If Value is an object (CloudFormation function), we need to resolve it
      // For now, we'll skip these as they should have been resolved by CloudFormation
      if (typeof envVar.Value === 'string') {
        runtimeEnvVars[envVar.Name] = envVar.Value;
      } else {
        console.warn('Skipping env var with non-string value:', envVar.Name);
      }
    }

    // Create App Runner service
    console.log('Creating App Runner service');
    const createServiceCommand = new CreateServiceCommand({
      ServiceName: appRunnerRequest.serviceName,
      SourceConfiguration: {
        AuthenticationConfiguration: {
          AccessRoleArn: appRunnerRequest.roleArns!.accessRoleArn,
        },
        ImageRepository: {
          ImageIdentifier: appRunnerRequest.imageUrl,
          ImageRepositoryType: 'ECR',
          ImageConfiguration: {
            Port: String(appRunnerRequest.port),
            RuntimeEnvironmentVariables: runtimeEnvVars,
          },
        },
      },
      InstanceConfiguration: {
        Cpu: appRunnerRequest.cpu,
        Memory: appRunnerRequest.memory,
        InstanceRoleArn: appRunnerRequest.roleArns!.instanceRoleArn,
      },
      NetworkConfiguration: vpcConnectorArn ? {
        EgressConfiguration: {
          EgressType: 'VPC',
          VpcConnectorArn: vpcConnectorArn,
        },
      } : undefined,
      Tags: [
        { Key: 'service', Value: appRunnerRequest.serviceName },
        { Key: 'ManagedBy', Value: 'Prod' },
      ],
    });

    const createServiceResponse = await appRunnerClient.send(createServiceCommand);
    const service = createServiceResponse.Service;

    if (!service) {
      throw new Error('Failed to create App Runner service');
    }

    console.log('App Runner service created:', {
      serviceArn: service.ServiceArn,
      serviceId: service.ServiceId,
      serviceUrl: service.ServiceUrl,
      status: service.Status,
    });

    const response: CreateAppRunnerResponse = {
      success: true,
      serviceArn: service.ServiceArn!,
      serviceUrl: service.ServiceUrl!,
    };

    return new Response(JSON.stringify(response), {
      status: 200,
      headers: { 'Content-Type': 'application/json' },
    });

  } catch (error) {
    console.error('Error creating App Runner service:', error);
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
