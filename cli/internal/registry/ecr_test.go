package registry

import (
	"context"
	"encoding/base64"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ecr"
	ecrtypes "github.com/aws/aws-sdk-go-v2/service/ecr/types"
	"github.com/aws/smithy-go"
)

type fakeECR struct {
	createErr error
	token     string // base64
	authErr   error
	emptyAuth bool
}

func (f fakeECR) CreateRepository(context.Context, *ecr.CreateRepositoryInput, ...func(*ecr.Options)) (*ecr.CreateRepositoryOutput, error) {
	return &ecr.CreateRepositoryOutput{}, f.createErr
}

func (f fakeECR) GetAuthorizationToken(context.Context, *ecr.GetAuthorizationTokenInput, ...func(*ecr.Options)) (*ecr.GetAuthorizationTokenOutput, error) {
	if f.authErr != nil {
		return nil, f.authErr
	}
	if f.emptyAuth {
		return &ecr.GetAuthorizationTokenOutput{}, nil
	}
	return &ecr.GetAuthorizationTokenOutput{
		AuthorizationData: []ecrtypes.AuthorizationData{{AuthorizationToken: aws.String(f.token)}},
	}, nil
}

func newTestECR(api ecrAPI) *ecrRegistry {
	return &ecrRegistry{api: api, accountID: "123456789012", region: "us-east-1"}
}

const testECRHost = "123456789012.dkr.ecr.us-east-1.amazonaws.com"

func TestECRHostAndRef(t *testing.T) {
	r := newTestECR(nil)
	if r.host() != testECRHost {
		t.Errorf("host = %q, want %q", r.host(), testECRHost)
	}
	if ref, err := r.Ref("my-app", "t"); err != nil || ref != testECRHost+"/my-app:t" {
		t.Errorf("Ref = %q, err %v", ref, err)
	}
	if _, err := r.Ref("Bad/Name", "t"); err == nil {
		t.Error("invalid project name should error")
	}
	if _, err := r.Ref("my-app", "bad tag"); err == nil {
		t.Error("invalid tag should error")
	}
}

func TestECRCredentials(t *testing.T) {
	token := base64.StdEncoding.EncodeToString([]byte("AWS:secret-pw"))
	c, err := newTestECR(fakeECR{token: token}).Credentials(context.Background(), "my-app")
	if err != nil {
		t.Fatalf("Credentials: %v", err)
	}
	if c.URL != testECRHost || c.AuthServer != testECRHost || c.Repository != "my-app" || c.Username != "AWS" || c.Token != "secret-pw" {
		t.Errorf("unexpected credentials (token redacted): %v", c)
	}
}

func TestECRCredentialsRepoAlreadyExists(t *testing.T) {
	token := base64.StdEncoding.EncodeToString([]byte("AWS:pw"))
	// Wrap in *smithy.OperationError exactly as the AWS SDK does on the wire, so
	// this proves the errors.As unwrap that production actually relies on.
	wrapped := &smithy.OperationError{
		ServiceID:     "ECR",
		OperationName: "CreateRepository",
		Err:           &ecrtypes.RepositoryAlreadyExistsException{},
	}
	r := newTestECR(fakeECR{token: token, createErr: wrapped})
	if _, err := r.Credentials(context.Background(), "my-app"); err != nil {
		t.Errorf("RepositoryAlreadyExists must be tolerated, got %v", err)
	}
}

func TestECRCredentialsNoAuthData(t *testing.T) {
	r := newTestECR(fakeECR{emptyAuth: true})
	if _, err := r.Credentials(context.Background(), "my-app"); err == nil {
		t.Error("empty AuthorizationData should error")
	}
}

func TestECRCredentialsCreateFails(t *testing.T) {
	r := newTestECR(fakeECR{createErr: errors.New("access denied")})
	if _, err := r.Credentials(context.Background(), "my-app"); err == nil {
		t.Error("a non-already-exists CreateRepository error should propagate")
	}
}

func TestDecodeECRToken(t *testing.T) {
	if u, p, err := decodeECRToken(base64.StdEncoding.EncodeToString([]byte("AWS:hunter2"))); err != nil || u != "AWS" || p != "hunter2" {
		t.Errorf("decode = (%q, %q, %v)", u, p, err)
	}
	if _, _, err := decodeECRToken("!!!not-base64"); err == nil {
		t.Error("invalid base64 should error")
	}
	if _, _, err := decodeECRToken(base64.StdEncoding.EncodeToString([]byte("no-colon"))); err == nil {
		t.Error("token without a colon should error")
	}
}
