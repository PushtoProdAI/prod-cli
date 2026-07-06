package gcprun

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	goerr "github.com/go-errors/errors"
	"golang.org/x/oauth2"
	"google.golang.org/api/cloudresourcemanager/v1"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
	secretmanager "google.golang.org/api/secretmanager/v1"
)

const secretAccessorRole = "roles/secretmanager.secretAccessor"

// secretManager provisions Secret Manager secrets and grants the Cloud Run runtime
// service account access, so sensitive env vars are stored as secrets and referenced,
// not set inline on the service.
type secretManager struct {
	sm      *secretmanager.Service
	crm     *cloudresourcemanager.Service
	project string
}

func newSecretManager(ctx context.Context, ts oauth2.TokenSource, project string) (*secretManager, error) {
	sm, err := secretmanager.NewService(ctx, option.WithTokenSource(ts))
	if err != nil {
		return nil, goerr.Errorf("failed to build Secret Manager client: %w", err)
	}
	crm, err := cloudresourcemanager.NewService(ctx, option.WithTokenSource(ts))
	if err != nil {
		return nil, goerr.Errorf("failed to build Resource Manager client: %w", err)
	}
	return &secretManager{sm: sm, crm: crm, project: project}, nil
}

// secretID builds a Secret Manager-valid id ([A-Za-z0-9_-], ≤255) from the app and
// var names.
func secretID(app, varName string) string {
	var b strings.Builder
	for _, r := range app + "-" + varName {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	id := b.String()
	if len(id) > 255 {
		id = id[:255]
	}
	return id
}

// EnsureSecret creates the secret if absent and adds value as a new version. Returns
// the secret resource path (projects/<p>/secrets/<id>) for a SecretKeyRef.
func (s *secretManager) EnsureSecret(ctx context.Context, id, value string) (string, error) {
	parent := "projects/" + s.project
	name := parent + "/secrets/" + id

	_, err := s.sm.Projects.Secrets.Create(parent, &secretmanager.Secret{
		Replication: &secretmanager.Replication{Automatic: &secretmanager.Automatic{}},
	}).SecretId(id).Context(ctx).Do()
	if err != nil && !isAlreadyExists(err) {
		return "", goerr.Errorf("failed to create secret %q (is secretmanager.googleapis.com enabled?): %w", id, err)
	}

	if _, err := s.sm.Projects.Secrets.AddVersion(name, &secretmanager.AddSecretVersionRequest{
		Payload: &secretmanager.SecretPayload{Data: base64.StdEncoding.EncodeToString([]byte(value))},
	}).Context(ctx).Do(); err != nil {
		return "", goerr.Errorf("failed to add a version to secret %q: %w", id, err)
	}
	return name, nil
}

// runtimeServiceAccount resolves the account the Cloud Run service runs as. A new
// prod-created service uses the project's default compute service account; resolving
// the project number builds its email. On a fresh project that never enabled Compute
// Engine this account may not exist yet — the IAM grant then fails with a clear error.
func (s *secretManager) runtimeServiceAccount(ctx context.Context) (string, error) {
	proj, err := s.crm.Projects.Get(s.project).Context(ctx).Do()
	if err != nil {
		return "", goerr.Errorf("failed to resolve the GCP project number for %q: %w", s.project, err)
	}
	if proj.ProjectNumber == 0 {
		return "", goerr.Errorf("could not determine the project number for %q", s.project)
	}
	return fmt.Sprintf("%d-compute@developer.gserviceaccount.com", proj.ProjectNumber), nil
}

// GrantAccessor grants serviceAccount the secretAccessor role on the secret via a
// read-modify-write of the secret's IAM policy — NOT a blind replace, which would wipe
// any other accessors. No-op if already granted.
func (s *secretManager) GrantAccessor(ctx context.Context, secretName, serviceAccount string) error {
	member := "serviceAccount:" + serviceAccount
	policy, err := s.sm.Projects.Secrets.GetIamPolicy(secretName).Context(ctx).Do()
	if err != nil {
		return goerr.Errorf("failed to read the IAM policy for %q: %w", secretName, err)
	}
	policy.Bindings = addMember(policy.Bindings, secretAccessorRole, member)
	if _, err := s.sm.Projects.Secrets.SetIamPolicy(secretName, &secretmanager.SetIamPolicyRequest{Policy: policy}).Context(ctx).Do(); err != nil {
		return goerr.Errorf("failed to grant secret access to %q (does the runtime service account exist?): %w", serviceAccount, err)
	}
	return nil
}

// addMember returns bindings with member added to role's binding (creating it if
// absent); a no-op if member is already present. Pure, so it's unit-testable.
func addMember(bindings []*secretmanager.Binding, role, member string) []*secretmanager.Binding {
	for _, b := range bindings {
		if b.Role == role {
			for _, m := range b.Members {
				if m == member {
					return bindings // already granted
				}
			}
			b.Members = append(b.Members, member)
			return bindings
		}
	}
	return append(bindings, &secretmanager.Binding{Role: role, Members: []string{member}})
}

// isAlreadyExists reports whether err is a Secret Manager 409 (secret already exists),
// which EnsureSecret treats as success before adding a new version.
func isAlreadyExists(err error) bool {
	var gerr *googleapi.Error
	return errors.As(err, &gerr) && gerr.Code == 409
}
