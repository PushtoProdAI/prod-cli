import {
  STSClient,
  AssumeRoleCommand,
} from "npm:@aws-sdk/client-sts";
import {
  ECRClient,
  GetAuthorizationTokenCommand,
  DescribeRepositoriesCommand,
  CreateRepositoryCommand,
  RepositoryNotFoundException
} from "npm:@aws-sdk/client-ecr";

export interface EcrRepoResp {
  exists: boolean;
  created?: boolean;
  repositoryName: string;
  repositoryUri?: string;
}

export interface EcrTokenResp {
  dockerAuthToken: string;
  dockerAuthUsername: string;
  dockerRepo: string;
  proxyEndpoint: string;
  expiresAt: Date;
  accountId: string;
}

export async function ecrTokenRequest(
  tenantId: string,
  repoName: string,
  roleArn: string,
  region?: string,
  externalId?: string | null,
): Promise<EcrTokenResp | Error> {
  if (!tenantId || !roleArn) {
    return new Error("Missing tenantId or roleArn");
  }

  const awsRegion = region || Deno.env.get("AWS_REGION") || "us-east-1";
  const sanitizedRepoName = repoName.replace(/\s+/g, '-');
  const fullRepoName = `${tenantId}-${sanitizedRepoName}`;

  try {
    const stsClient = new STSClient({ 
      region: awsRegion,
      credentials: {
        accessKeyId: Deno.env.get("AWS_ACCESS_KEY_ID") || "",
        secretAccessKey: Deno.env.get("AWS_SECRET_ACCESS_KEY") || "",
      }
    });

    // Build assume role command with optional ExternalId
    const assumeRoleParams: any = {
      RoleArn: roleArn,
      RoleSessionName: `session-${tenantId}`,
      DurationSeconds: 3600,
      Tags: [{ Key: "tenant", Value: tenantId }],
      TransitiveTagKeys: ["tenant"]
    };

    // Add ExternalId if provided (required for customer AWS accounts)
    if (externalId) {
      assumeRoleParams.ExternalId = externalId;
    }

    const assumeRoleCommand = new AssumeRoleCommand(assumeRoleParams);

    const { Credentials, AssumedRoleUser } = await stsClient.send(assumeRoleCommand);

    if (!Credentials) {
      return new Error("No credentials returned from STS");
    }

    const ecrClient = new ECRClient({
      region: awsRegion,
      credentials: {
        accessKeyId: Credentials.AccessKeyId!,
        secretAccessKey: Credentials.SecretAccessKey!,
        sessionToken: Credentials.SessionToken,
      },
    });

    let repositoryUri = "";
    try {
      const describeResult = await ecrClient.send(
        new DescribeRepositoriesCommand({
          repositoryNames: [fullRepoName],
        }),
      );
      if (describeResult.repositories && describeResult.repositories.length > 0) {
        repositoryUri = describeResult.repositories[0].repositoryUri || "";
      }
    } catch (error) {
      if (!(error instanceof RepositoryNotFoundException)) {
        return error instanceof Error ? error : new Error("Unknown error occurred");
      }
    }

    const authResult = await ecrClient.send(new GetAuthorizationTokenCommand({}));

    const authData = authResult.authorizationData?.[0];
    if (!authData || !authData.authorizationToken) {
      return new Error("No authorization token returned");
    }

    const authToken = authData.authorizationToken;
    const decoded = atob(authToken);
    const [, password] = decoded.split(":");

    const assumedArn = AssumedRoleUser.Arn;
    const accountIdMatch = assumedArn.match(/arn:aws:sts::(\d+):/);
    const accountId = accountIdMatch ? accountIdMatch[1] : "";

    return {
      dockerAuthToken: password,
      dockerAuthUsername: "AWS",
      dockerRepo: fullRepoName,
      proxyEndpoint: authData.proxyEndpoint,
      expiresAt: authData.expiresAt,
      accountId: accountId,
    };
  } catch (err) {
    console.error("ECR token request error:", err);
    return err instanceof Error ? err : new Error("Unknown error occurred");
  }
}
export async function checkAndCreateECRRepo(
  tenantId: string,
  repoName: string,
  roleArn: string,
  region?: string,
  externalId?: string | null,
): Promise<EcrRepoResp | Error> {
  if (!tenantId || !repoName || !roleArn) {
    return new Error('Missing userId, repoName, or roleArn');
  }

  const awsRegion = region || Deno.env.get('AWS_REGION') || 'us-east-1';
  const sanitizedRepoName = repoName.replace(/\s+/g, '-');
  const fullRepoName = `${tenantId}-${sanitizedRepoName}`;

  try {
    const stsClient = new STSClient({
      region: awsRegion,
      credentials: {
        accessKeyId: Deno.env.get('AWS_ACCESS_KEY_ID') || '',
        secretAccessKey: Deno.env.get('AWS_SECRET_ACCESS_KEY') || '',
      },
    });

    // Build assume role command with optional ExternalId
    const assumeRoleParams: any = {
      RoleArn: roleArn,
      RoleSessionName: `session-${tenantId}`,
      DurationSeconds: 3600,
      Tags: [{ Key: 'tenant', Value: tenantId }],
      TransitiveTagKeys: ['tenant'],
    };

    // Add ExternalId if provided (required for customer AWS accounts)
    if (externalId) {
      assumeRoleParams.ExternalId = externalId;
    }

    const assumeRoleCommand = new AssumeRoleCommand(assumeRoleParams);

    const { Credentials } = await stsClient.send(assumeRoleCommand);

    if (!Credentials) {
      return new Error('No credentials returned from STS');
    }

    const ecrClient = new ECRClient({
      region: awsRegion,
      credentials: {
        accessKeyId: Credentials.AccessKeyId!,
        secretAccessKey: Credentials.SecretAccessKey!,
        sessionToken: Credentials.SessionToken,
      },
    });

    try {
      const describeResult = await ecrClient.send(
        new DescribeRepositoriesCommand({
          repositoryNames: [fullRepoName],
        }),
      );

      if (
        describeResult.repositories &&
        describeResult.repositories.length > 0
      ) {
        return {
          exists: true,
          repositoryName: fullRepoName,
          repositoryUri: describeResult.repositories[0].repositoryUri,
        };
      }
    } catch (error) {
      if (error instanceof RepositoryNotFoundException) {
        const createResult = await ecrClient.send(
          new CreateRepositoryCommand({
            repositoryName: fullRepoName,
            tags: [
              {
                Key: 'tenant',
                Value: tenantId,
              },
            ],
          }),
        );

        return {
          exists: false,
          created: true,
          repositoryName: fullRepoName,
          repositoryUri: createResult.repository?.repositoryUri,
        };
      }
      return error instanceof Error ? error : new Error("Unknown error occurred");
    }

    return { exists: false, repositoryName: fullRepoName };
  } catch (err) {
    console.error('ECR repository check/create error:', err);
    return err instanceof Error ? err : new Error('Unknown error occurred');
  }
}

