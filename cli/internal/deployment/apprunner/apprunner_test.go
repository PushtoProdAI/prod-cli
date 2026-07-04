package apprunner

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	arsdk "github.com/aws/aws-sdk-go-v2/service/apprunner"
	artypes "github.com/aws/aws-sdk-go-v2/service/apprunner/types"
	iamsdk "github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
	"github.com/aws/smithy-go"
)

type fakeIAM struct {
	createErr error
	roleArn   string
	created   bool
	attached  bool
}

func (f *fakeIAM) CreateRole(context.Context, *iamsdk.CreateRoleInput, ...func(*iamsdk.Options)) (*iamsdk.CreateRoleOutput, error) {
	f.created = true
	return &iamsdk.CreateRoleOutput{}, f.createErr
}

func (f *fakeIAM) AttachRolePolicy(context.Context, *iamsdk.AttachRolePolicyInput, ...func(*iamsdk.Options)) (*iamsdk.AttachRolePolicyOutput, error) {
	f.attached = true
	return &iamsdk.AttachRolePolicyOutput{}, nil
}

func (f *fakeIAM) GetRole(context.Context, *iamsdk.GetRoleInput, ...func(*iamsdk.Options)) (*iamsdk.GetRoleOutput, error) {
	return &iamsdk.GetRoleOutput{Role: &iamtypes.Role{Arn: aws.String(f.roleArn)}}, nil
}

type fakeAR struct {
	existing     map[string]string // name -> arn
	created      bool
	createArn    string
	updatedArn   string // arn passed to UpdateService
	updatedImage string // image ref passed to UpdateService
}

func (f *fakeAR) ListServices(context.Context, *arsdk.ListServicesInput, ...func(*arsdk.Options)) (*arsdk.ListServicesOutput, error) {
	var list []artypes.ServiceSummary
	for name, arn := range f.existing {
		list = append(list, artypes.ServiceSummary{ServiceName: aws.String(name), ServiceArn: aws.String(arn)})
	}
	return &arsdk.ListServicesOutput{ServiceSummaryList: list}, nil
}

func (f *fakeAR) CreateService(context.Context, *arsdk.CreateServiceInput, ...func(*arsdk.Options)) (*arsdk.CreateServiceOutput, error) {
	f.created = true
	return &arsdk.CreateServiceOutput{Service: &artypes.Service{ServiceArn: aws.String(f.createArn)}}, nil
}

func (f *fakeAR) UpdateService(_ context.Context, in *arsdk.UpdateServiceInput, _ ...func(*arsdk.Options)) (*arsdk.UpdateServiceOutput, error) {
	f.updatedArn = aws.ToString(in.ServiceArn)
	f.updatedImage = aws.ToString(in.SourceConfiguration.ImageRepository.ImageIdentifier)
	return &arsdk.UpdateServiceOutput{}, nil
}

func TestEnsureAccessRole(t *testing.T) {
	t.Run("creates and returns arn", func(t *testing.T) {
		iam := &fakeIAM{roleArn: "arn:aws:iam::123:role/prod-apprunner-ecr-access"}
		d := &Deployer{iam: iam}
		arn, err := d.EnsureAccessRole(context.Background())
		if err != nil || arn != iam.roleArn {
			t.Fatalf("arn=%q err=%v", arn, err)
		}
		if !iam.created || !iam.attached {
			t.Errorf("expected create+attach, got created=%v attached=%v", iam.created, iam.attached)
		}
	})

	t.Run("tolerates already-exists (wrapped as the SDK does)", func(t *testing.T) {
		iam := &fakeIAM{
			roleArn:   "arn:aws:iam::123:role/prod-apprunner-ecr-access",
			createErr: &smithy.OperationError{ServiceID: "IAM", OperationName: "CreateRole", Err: &iamtypes.EntityAlreadyExistsException{}},
		}
		d := &Deployer{iam: iam}
		if arn, err := d.EnsureAccessRole(context.Background()); err != nil || arn == "" {
			t.Fatalf("already-exists should succeed: arn=%q err=%v", arn, err)
		}
		if !iam.attached {
			t.Error("policy should still be (re)attached on an existing role")
		}
	})

	t.Run("propagates other create errors", func(t *testing.T) {
		d := &Deployer{iam: &fakeIAM{createErr: errors.New("access denied")}}
		if _, err := d.EnsureAccessRole(context.Background()); err == nil {
			t.Error("a non-already-exists error should propagate")
		}
	})
}

func TestDeploy(t *testing.T) {
	cfg := ServiceConfig{Name: "my-app", ImageRef: "123.dkr.ecr.us-east-1.amazonaws.com/my-app:t", AccessRoleArn: "arn:role", Port: "8080", CPU: "1024", Memory: "2048"}

	t.Run("creates a new service", func(t *testing.T) {
		ar := &fakeAR{createArn: "arn:aws:apprunner:...:service/my-app"}
		d := &Deployer{ar: ar}
		arn, err := d.Deploy(context.Background(), cfg)
		if err != nil || arn != ar.createArn {
			t.Fatalf("arn=%q err=%v", arn, err)
		}
		if !ar.created || ar.updatedArn != "" {
			t.Errorf("expected CreateService, got created=%v updatedArn=%q", ar.created, ar.updatedArn)
		}
	})

	t.Run("updates an existing service with the new image", func(t *testing.T) {
		ar := &fakeAR{existing: map[string]string{"my-app": "arn:existing"}}
		d := &Deployer{ar: ar}
		arn, err := d.Deploy(context.Background(), cfg)
		if err != nil || arn != "arn:existing" {
			t.Fatalf("arn=%q err=%v", arn, err)
		}
		if ar.created || ar.updatedArn != "arn:existing" {
			t.Errorf("expected UpdateService on existing, got created=%v updatedArn=%q", ar.created, ar.updatedArn)
		}
		// The redeploy must carry the NEW image ref, not silently reuse the old one.
		if ar.updatedImage != cfg.ImageRef {
			t.Errorf("UpdateService image = %q, want the new ref %q", ar.updatedImage, cfg.ImageRef)
		}
	})
}

func TestCreateInput(t *testing.T) {
	in := CreateInput(ServiceConfig{
		Name: "my-app", ImageRef: "123.dkr.ecr.us-east-1.amazonaws.com/my-app:t",
		AccessRoleArn: "arn:role", Port: "8080", CPU: "1024", Memory: "2048",
		StartCommand: "node index.js", EnvVars: map[string]string{"FOO": "bar"},
	})

	if aws.ToString(in.ServiceName) != "my-app" {
		t.Errorf("ServiceName = %q", aws.ToString(in.ServiceName))
	}
	img := in.SourceConfiguration.ImageRepository
	if img.ImageRepositoryType != artypes.ImageRepositoryTypeEcr {
		t.Errorf("ImageRepositoryType = %v, want ECR", img.ImageRepositoryType)
	}
	if aws.ToString(img.ImageIdentifier) != "123.dkr.ecr.us-east-1.amazonaws.com/my-app:t" {
		t.Errorf("ImageIdentifier = %q", aws.ToString(img.ImageIdentifier))
	}
	if aws.ToString(in.SourceConfiguration.AuthenticationConfiguration.AccessRoleArn) != "arn:role" {
		t.Error("AccessRoleArn not set")
	}
	if aws.ToString(img.ImageConfiguration.Port) != "8080" || aws.ToString(img.ImageConfiguration.StartCommand) != "node index.js" {
		t.Error("port/start command not set")
	}
	if img.ImageConfiguration.RuntimeEnvironmentVariables["FOO"] != "bar" {
		t.Error("env var not set")
	}
	if aws.ToString(in.InstanceConfiguration.Cpu) != "1024" || aws.ToString(in.InstanceConfiguration.Memory) != "2048" {
		t.Error("cpu/memory not set")
	}
}
