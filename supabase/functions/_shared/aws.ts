import {
  STSClient,
  AssumeRoleCommand,
} from "npm:@aws-sdk/client-sts";
import {
  ECRClient,
  GetAuthorizationTokenCommand,
} from "npm:@aws-sdk/client-ecr";

export interface EcrTokenResp {
  dockerAuthToken: string;
  proxyEndpoint: string;
  expiresAt: Date;
}

export async function ecrTokenRequest(tenantId: string, roleArn: string): Promise<EcrTokenResp | Error> {
  if (!tenantId || !roleArn) {
    return new Error("Missing tenantId or roleArn");
  }

  const region = Deno.env.get("AWS_REGION") || "us-east-1";

  try {
    const stsClient = new STSClient({ 
      region,
      credentials: {
        accessKeyId: Deno.env.get("AWS_ACCESS_KEY_ID") || "",
        secretAccessKey: Deno.env.get("AWS_SECRET_ACCESS_KEY") || "",
      }
    });

    const assumeRoleCommand = new AssumeRoleCommand({
      RoleArn: roleArn,
      RoleSessionName: `session-${tenantId}`,
      DurationSeconds: 3600,
      Tags: [{ Key: "tenant", Value: tenantId }],
      TransitiveTagKeys: ["tenant"]
    });

    const { Credentials } = await stsClient.send(assumeRoleCommand);

    if (!Credentials) {
      return new Error("No credentials returned from STS");
    }

    const ecrClient = new ECRClient({
      region,
      credentials: {
        accessKeyId: Credentials.AccessKeyId!,
        secretAccessKey: Credentials.SecretAccessKey!,
        sessionToken: Credentials.SessionToken,
      },
    });

    const authResult = await ecrClient.send(new GetAuthorizationTokenCommand({}));

    const authData = authResult.authorizationData?.[0];
    if (!authData || !authData.authorizationToken) {
      return new Error("No authorization token returned");
    }

    const authToken = authData.authorizationToken;
    const decoded = atob(authToken);
    const [, password] = decoded.split(":");

    return {
      dockerAuthToken: password,
      proxyEndpoint: authData.proxyEndpoint,
      expiresAt: authData.expiresAt,
    };
  } catch (err) {
    console.error("ECR token request error:", err);
    return err instanceof Error ? err : new Error("Unknown error occurred");
  }
}
