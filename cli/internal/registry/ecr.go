package registry

import (
	"context"
	"encoding/base64"
	stderrors "errors"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ecr"
	ecrtypes "github.com/aws/aws-sdk-go-v2/service/ecr/types"
	"github.com/go-errors/errors"
)

// ecrAPI is the subset of the ECR client used here — injectable for tests.
type ecrAPI interface {
	CreateRepository(ctx context.Context, in *ecr.CreateRepositoryInput, opts ...func(*ecr.Options)) (*ecr.CreateRepositoryOutput, error)
	GetAuthorizationToken(ctx context.Context, in *ecr.GetAuthorizationTokenInput, opts ...func(*ecr.Options)) (*ecr.GetAuthorizationTokenOutput, error)
}

// ecrRegistry is an Amazon ECR registry in the user's own AWS account. Unlike the
// host-based registries, it makes AWS API calls in Credentials (ensure repo +
// fetch a short-lived token), so it's constructed from the user's aws.Config
// rather than via FromEnv.
type ecrRegistry struct {
	api       ecrAPI
	accountID string
	region    string
}

var _ Registry = (*ecrRegistry)(nil)

// NewECR builds an ECR registry from the user's AWS config and account id
// (both from auth.AWSAuth.Config).
func NewECR(cfg aws.Config, accountID string) Registry {
	return &ecrRegistry{api: ecr.NewFromConfig(cfg), accountID: accountID, region: cfg.Region}
}

func (r *ecrRegistry) Name() string { return "ecr" }

// host returns the ECR registry host. Standard AWS partitions only — GovCloud,
// China, and FIPS endpoints differ and aren't handled yet.
func (r *ecrRegistry) host() string {
	return fmt.Sprintf("%s.dkr.ecr.%s.amazonaws.com", r.accountID, r.region)
}

// repo validates/normalizes a project name into an ECR repository name.
func (r *ecrRegistry) repo(project string) (string, error) {
	p := strings.ToLower(strings.TrimSpace(project))
	if !projectNameRe.MatchString(p) {
		return "", errors.Errorf("invalid project name %q for an ECR repository: use lowercase letters, digits, and . _ -", project)
	}
	return p, nil
}

func (r *ecrRegistry) Ref(project, tag string) (string, error) {
	repo, err := r.repo(project)
	if err != nil {
		return "", err
	}
	if !tagRe.MatchString(tag) {
		return "", errors.Errorf("invalid image tag %q", tag)
	}
	return fmt.Sprintf("%s/%s:%s", r.host(), repo, tag), nil
}

// Credentials ensures the ECR repository exists (idempotent) and returns a
// short-lived authorization token for pushing.
func (r *ecrRegistry) Credentials(ctx context.Context, project string) (Credentials, error) {
	repo, err := r.repo(project)
	if err != nil {
		return Credentials{}, err
	}

	// Ensure the repository exists; "already exists" is success.
	if _, err := r.api.CreateRepository(ctx, &ecr.CreateRepositoryInput{RepositoryName: aws.String(repo)}); err != nil {
		var exists *ecrtypes.RepositoryAlreadyExistsException
		if !stderrors.As(err, &exists) {
			return Credentials{}, errors.Errorf("failed to ensure ECR repository %q: %w", repo, err)
		}
	}

	out, err := r.api.GetAuthorizationToken(ctx, &ecr.GetAuthorizationTokenInput{})
	if err != nil {
		return Credentials{}, errors.Errorf("failed to get ECR authorization token: %w", err)
	}
	if len(out.AuthorizationData) == 0 || out.AuthorizationData[0].AuthorizationToken == nil {
		return Credentials{}, errors.Errorf("ECR returned no authorization data")
	}

	user, pass, err := decodeECRToken(aws.ToString(out.AuthorizationData[0].AuthorizationToken))
	if err != nil {
		return Credentials{}, err
	}

	return Credentials{
		URL:        r.host(),
		AuthServer: r.host(),
		Repository: repo,
		Username:   user,
		Token:      pass,
	}, nil
}

// decodeECRToken decodes a base64 "user:password" ECR authorization token.
func decodeECRToken(token string) (user, pass string, err error) {
	raw, err := base64.StdEncoding.DecodeString(token)
	if err != nil {
		return "", "", errors.Errorf("failed to decode ECR token: %w", err)
	}
	u, p, ok := strings.Cut(string(raw), ":")
	if !ok {
		return "", "", errors.Errorf("malformed ECR authorization token")
	}
	return u, p, nil
}
