package apprunner

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"sort"

	"github.com/aws/aws-sdk-go-v2/aws"
	iamsdk "github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
	smsdk "github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	smtypes "github.com/aws/aws-sdk-go-v2/service/secretsmanager/types"
	"github.com/go-errors/errors"
)

const (
	secretsReadPolicyName = "prod-secrets-read"
	// instanceAssumeRolePolicy lets a running App Runner service assume the role.
	instanceAssumeRolePolicy = `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"Service":"tasks.apprunner.amazonaws.com"},"Action":"sts:AssumeRole"}]}`
)

// instanceRoleName is the App Runner instance role for a service. It MUST be
// per-service: the inline secrets policy is replaced wholesale on each deploy, so
// a single shared role would let one service's deploy strip another service's
// secret access. Capped at IAM's 64-char role-name limit.
func instanceRoleName(service string) string {
	name := "prod-apprunner-instance-" + service
	if len(name) > 64 {
		name = name[:64]
	}
	return name
}

// ensureSecrets stores each sensitive var in Secrets Manager (creating or, if it
// already exists, updating its value) and returns a map of env-var name →
// secret ARN for use as App Runner RuntimeEnvironmentSecrets.
func (d *Deployer) ensureSecrets(ctx context.Context, service string, secrets map[string]string) (map[string]string, error) {
	arns := make(map[string]string, len(secrets))
	for _, env := range sortedKeys(secrets) {
		name := secretName(service, env)
		out, err := d.sm.CreateSecret(ctx, &smsdk.CreateSecretInput{
			Name:         aws.String(name),
			SecretString: aws.String(secrets[env]),
		})
		if err != nil {
			var exists *smtypes.ResourceExistsException
			if !stderrors.As(err, &exists) {
				return nil, errors.Errorf("failed to create secret for %s: %w", env, err)
			}
			put, perr := d.sm.PutSecretValue(ctx, &smsdk.PutSecretValueInput{
				SecretId:     aws.String(name),
				SecretString: aws.String(secrets[env]),
			})
			if perr != nil {
				return nil, errors.Errorf("failed to update secret for %s: %w", env, perr)
			}
			arns[env] = aws.ToString(put.ARN)
			continue
		}
		arns[env] = aws.ToString(out.ARN)
	}
	return arns, nil
}

// ensureInstanceRole creates (or finds) the App Runner instance role and grants
// it read access to exactly the given secret ARNs. Returns the role ARN.
func (d *Deployer) ensureInstanceRole(ctx context.Context, service string, secretARNs []string) (string, error) {
	roleName := instanceRoleName(service)
	_, err := d.iam.CreateRole(ctx, &iamsdk.CreateRoleInput{
		RoleName:                 aws.String(roleName),
		AssumeRolePolicyDocument: aws.String(instanceAssumeRolePolicy),
		Description:              aws.String("Lets your App Runner service read its secrets (created by prod)"),
	})
	if err != nil {
		var exists *iamtypes.EntityAlreadyExistsException
		if !stderrors.As(err, &exists) {
			return "", errors.Errorf("failed to create App Runner instance role: %w", err)
		}
	}

	policy, err := secretsReadPolicyDocument(secretARNs)
	if err != nil {
		return "", err
	}
	if _, err := d.iam.PutRolePolicy(ctx, &iamsdk.PutRolePolicyInput{
		RoleName:       aws.String(roleName),
		PolicyName:     aws.String(secretsReadPolicyName),
		PolicyDocument: aws.String(policy),
	}); err != nil {
		return "", errors.Errorf("failed to attach secrets-read policy: %w", err)
	}

	out, err := d.iam.GetRole(ctx, &iamsdk.GetRoleInput{RoleName: aws.String(roleName)})
	if err != nil {
		return "", errors.Errorf("failed to get App Runner instance role: %w", err)
	}
	if out.Role == nil {
		return "", errors.Errorf("App Runner instance role has no ARN")
	}
	return aws.ToString(out.Role.Arn), nil
}

// secretName is the Secrets Manager name for a service's env var, e.g.
// "prod/my-app/DATABASE_URL".
func secretName(service, env string) string {
	return fmt.Sprintf("prod/%s/%s", service, env)
}

// secretsReadPolicyDocument builds an IAM policy granting GetSecretValue on the
// given secret ARNs (JSON-marshalled so ARNs are escaped correctly).
func secretsReadPolicyDocument(arns []string) (string, error) {
	doc := map[string]any{
		"Version": "2012-10-17",
		"Statement": []map[string]any{{
			"Effect":   "Allow",
			"Action":   "secretsmanager:GetSecretValue",
			"Resource": arns,
		}},
	}
	b, err := json.Marshal(doc)
	if err != nil {
		return "", errors.Errorf("failed to build secrets-read policy: %w", err)
	}
	return string(b), nil
}

// arnValues returns the values of an env→ARN map, sorted for a stable policy.
func arnValues(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

// sortedKeys returns map keys sorted, for deterministic secret creation order.
func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
