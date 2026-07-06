package apprunner

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	arsdk "github.com/aws/aws-sdk-go-v2/service/apprunner"
	artypes "github.com/aws/aws-sdk-go-v2/service/apprunner/types"
	iamsdk "github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
	smsdk "github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	smtypes "github.com/aws/aws-sdk-go-v2/service/secretsmanager/types"
	"github.com/aws/smithy-go"
)

type fakeIAM struct {
	createErr    error
	created      bool
	attached     bool
	createdRoles []string          // RoleNames passed to CreateRole
	putPolicies  map[string]string // RoleName -> last PutRolePolicy document
}

func (f *fakeIAM) CreateRole(_ context.Context, in *iamsdk.CreateRoleInput, _ ...func(*iamsdk.Options)) (*iamsdk.CreateRoleOutput, error) {
	f.created = true
	f.createdRoles = append(f.createdRoles, aws.ToString(in.RoleName))
	return &iamsdk.CreateRoleOutput{}, f.createErr
}

func (f *fakeIAM) AttachRolePolicy(context.Context, *iamsdk.AttachRolePolicyInput, ...func(*iamsdk.Options)) (*iamsdk.AttachRolePolicyOutput, error) {
	f.attached = true
	return &iamsdk.AttachRolePolicyOutput{}, nil
}

func (f *fakeIAM) PutRolePolicy(_ context.Context, in *iamsdk.PutRolePolicyInput, _ ...func(*iamsdk.Options)) (*iamsdk.PutRolePolicyOutput, error) {
	if f.putPolicies == nil {
		f.putPolicies = map[string]string{}
	}
	f.putPolicies[aws.ToString(in.RoleName)] = aws.ToString(in.PolicyDocument)
	return &iamsdk.PutRolePolicyOutput{}, nil
}

// GetRole returns an ARN derived from the role name so distinct roles yield
// distinct ARNs (faithful enough for wiring assertions).
func (f *fakeIAM) GetRole(_ context.Context, in *iamsdk.GetRoleInput, _ ...func(*iamsdk.Options)) (*iamsdk.GetRoleOutput, error) {
	return &iamsdk.GetRoleOutput{Role: &iamtypes.Role{Arn: aws.String("arn:aws:iam::123:role/" + aws.ToString(in.RoleName))}}, nil
}

// fakeSecrets records secret creation; CreateSecret returns createErr[name] if set
// (e.g. a wrapped ResourceExistsException to exercise the PutSecretValue path).
type fakeSecrets struct {
	createErr map[string]error
	created   []string
	put       []string
}

func (f *fakeSecrets) CreateSecret(_ context.Context, in *smsdk.CreateSecretInput, _ ...func(*smsdk.Options)) (*smsdk.CreateSecretOutput, error) {
	name := aws.ToString(in.Name)
	if err := f.createErr[name]; err != nil {
		return nil, err
	}
	f.created = append(f.created, name)
	return &smsdk.CreateSecretOutput{ARN: aws.String("arn:secret:" + name)}, nil
}

func (f *fakeSecrets) PutSecretValue(_ context.Context, in *smsdk.PutSecretValueInput, _ ...func(*smsdk.Options)) (*smsdk.PutSecretValueOutput, error) {
	name := aws.ToString(in.SecretId)
	f.put = append(f.put, name)
	return &smsdk.PutSecretValueOutput{ARN: aws.String("arn:secret:" + name)}, nil
}

type fakeAR struct {
	existing      map[string]string // name -> arn
	created       bool
	createArn     string
	updatedArn    string                  // arn passed to UpdateService
	updatedImage  string                  // image ref passed to UpdateService
	statuses      []artypes.ServiceStatus // DescribeService returns these in order (last repeats)
	url           string
	describeCalls int
	lastCreate    *arsdk.CreateServiceInput
	lastUpdate    *arsdk.UpdateServiceInput
	deletedArn    string // arn passed to DeleteService
}

func (f *fakeAR) ListServices(context.Context, *arsdk.ListServicesInput, ...func(*arsdk.Options)) (*arsdk.ListServicesOutput, error) {
	var list []artypes.ServiceSummary
	for name, arn := range f.existing {
		list = append(list, artypes.ServiceSummary{ServiceName: aws.String(name), ServiceArn: aws.String(arn)})
	}
	return &arsdk.ListServicesOutput{ServiceSummaryList: list}, nil
}

func (f *fakeAR) DeleteService(_ context.Context, in *arsdk.DeleteServiceInput, _ ...func(*arsdk.Options)) (*arsdk.DeleteServiceOutput, error) {
	f.deletedArn = aws.ToString(in.ServiceArn)
	return &arsdk.DeleteServiceOutput{}, nil
}

func (f *fakeAR) CreateService(_ context.Context, in *arsdk.CreateServiceInput, _ ...func(*arsdk.Options)) (*arsdk.CreateServiceOutput, error) {
	f.created = true
	f.lastCreate = in
	return &arsdk.CreateServiceOutput{Service: &artypes.Service{ServiceArn: aws.String(f.createArn)}}, nil
}

func (f *fakeAR) UpdateService(_ context.Context, in *arsdk.UpdateServiceInput, _ ...func(*arsdk.Options)) (*arsdk.UpdateServiceOutput, error) {
	f.updatedArn = aws.ToString(in.ServiceArn)
	f.updatedImage = aws.ToString(in.SourceConfiguration.ImageRepository.ImageIdentifier)
	f.lastUpdate = in
	return &arsdk.UpdateServiceOutput{}, nil
}

func (f *fakeAR) DescribeService(context.Context, *arsdk.DescribeServiceInput, ...func(*arsdk.Options)) (*arsdk.DescribeServiceOutput, error) {
	i := f.describeCalls
	if i >= len(f.statuses) {
		i = len(f.statuses) - 1 // repeat the last status
	}
	f.describeCalls++
	return &arsdk.DescribeServiceOutput{Service: &artypes.Service{Status: f.statuses[i], ServiceUrl: aws.String(f.url)}}, nil
}

func TestEnsureAccessRole(t *testing.T) {
	t.Run("creates and returns arn", func(t *testing.T) {
		iam := &fakeIAM{}
		d := &Deployer{iam: iam}
		arn, err := d.EnsureAccessRole(context.Background())
		if err != nil || arn != "arn:aws:iam::123:role/prod-apprunner-ecr-access" {
			t.Fatalf("arn=%q err=%v", arn, err)
		}
		if !iam.created || !iam.attached {
			t.Errorf("expected create+attach, got created=%v attached=%v", iam.created, iam.attached)
		}
	})

	t.Run("tolerates already-exists (wrapped as the SDK does)", func(t *testing.T) {
		iam := &fakeIAM{
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

func TestDelete(t *testing.T) {
	t.Run("deletes the service by name", func(t *testing.T) {
		ar := &fakeAR{existing: map[string]string{"myapp": "arn:aws:apprunner:svc/myapp"}}
		d := &Deployer{ar: ar}
		if err := d.Delete(context.Background(), "myapp"); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		if ar.deletedArn != "arn:aws:apprunner:svc/myapp" {
			t.Errorf("DeleteService got arn %q, want the service's arn", ar.deletedArn)
		}
	})

	t.Run("no-op when the service is already gone", func(t *testing.T) {
		ar := &fakeAR{existing: map[string]string{}}
		d := &Deployer{ar: ar}
		if err := d.Delete(context.Background(), "gone"); err != nil {
			t.Errorf("Delete of a missing service should be nil, got %v", err)
		}
		if ar.deletedArn != "" {
			t.Errorf("DeleteService should not be called for a missing service")
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

func TestWaitForRunning(t *testing.T) {
	t.Run("returns the url once RUNNING", func(t *testing.T) {
		ar := &fakeAR{
			statuses: []artypes.ServiceStatus{
				artypes.ServiceStatusOperationInProgress,
				artypes.ServiceStatusOperationInProgress,
				artypes.ServiceStatusRunning,
			},
			url: "https://abc.us-east-1.awsapprunner.com",
		}
		d := &Deployer{ar: ar, pollInterval: time.Millisecond}
		url, err := d.WaitForRunning(context.Background(), "arn:svc")
		if err != nil || url != ar.url {
			t.Fatalf("url=%q err=%v", url, err)
		}
		if ar.describeCalls != 3 {
			t.Errorf("expected 3 polls, got %d", ar.describeCalls)
		}
	})

	// Every non-RUNNING terminal state must error, not spin.
	for _, st := range []artypes.ServiceStatus{
		artypes.ServiceStatusCreateFailed, artypes.ServiceStatusDeleteFailed,
		artypes.ServiceStatusDeleted, artypes.ServiceStatusPaused,
	} {
		t.Run("errors on "+string(st), func(t *testing.T) {
			ar := &fakeAR{statuses: []artypes.ServiceStatus{st}, url: "x"}
			if _, err := (&Deployer{ar: ar}).WaitForRunning(context.Background(), "arn:svc"); err == nil {
				t.Errorf("%s should error", st)
			}
		})
	}

	t.Run("errors if RUNNING without a URL", func(t *testing.T) {
		ar := &fakeAR{statuses: []artypes.ServiceStatus{artypes.ServiceStatusRunning}} // url empty
		if _, err := (&Deployer{ar: ar}).WaitForRunning(context.Background(), "arn:svc"); err == nil {
			t.Error("RUNNING with an empty URL should error")
		}
	})

	t.Run("respects context cancellation", func(t *testing.T) {
		ar := &fakeAR{statuses: []artypes.ServiceStatus{artypes.ServiceStatusOperationInProgress}}
		d := &Deployer{ar: ar, pollInterval: time.Hour} // would block; ctx must win
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := d.WaitForRunning(ctx, "arn:svc"); err == nil {
			t.Error("a cancelled context should stop the wait with an error")
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

func TestEnsureSecrets(t *testing.T) {
	t.Run("creates secrets and returns arns", func(t *testing.T) {
		sm := &fakeSecrets{}
		arns, err := (&Deployer{sm: sm}).ensureSecrets(context.Background(), "my-app",
			map[string]string{"DATABASE_URL": "postgres://x", "API_KEY": "k"})
		if err != nil {
			t.Fatal(err)
		}
		if arns["DATABASE_URL"] != "arn:secret:prod/my-app/DATABASE_URL" || arns["API_KEY"] != "arn:secret:prod/my-app/API_KEY" {
			t.Errorf("unexpected arns: %v", arns)
		}
		if len(sm.created) != 2 || len(sm.put) != 0 {
			t.Errorf("expected 2 creates / 0 puts, got created=%v put=%v", sm.created, sm.put)
		}
	})

	t.Run("updates a secret that already exists", func(t *testing.T) {
		name := "prod/my-app/DATABASE_URL"
		sm := &fakeSecrets{createErr: map[string]error{
			name: &smithy.OperationError{ServiceID: "SecretsManager", OperationName: "CreateSecret", Err: &smtypes.ResourceExistsException{}},
		}}
		arns, err := (&Deployer{sm: sm}).ensureSecrets(context.Background(), "my-app",
			map[string]string{"DATABASE_URL": "postgres://x"})
		if err != nil {
			t.Fatal(err)
		}
		if arns["DATABASE_URL"] != "arn:secret:"+name {
			t.Errorf("arn = %q", arns["DATABASE_URL"])
		}
		if len(sm.put) != 1 || sm.put[0] != name {
			t.Errorf("expected PutSecretValue on the existing secret, got %v", sm.put)
		}
	})
}

func TestSecretsReadPolicyDocument(t *testing.T) {
	doc, err := secretsReadPolicyDocument([]string{"arn:a", "arn:b"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(doc, "secretsmanager:GetSecretValue") || !strings.Contains(doc, "arn:a") || !strings.Contains(doc, "arn:b") {
		t.Errorf("policy missing action/resources: %s", doc)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(doc), &parsed); err != nil {
		t.Errorf("policy is not valid JSON: %v", err)
	}
}

func TestDeployWithSecrets(t *testing.T) {
	ar := &fakeAR{createArn: "arn:svc"}
	iam := &fakeIAM{}
	sm := &fakeSecrets{}
	d := &Deployer{ar: ar, iam: iam, sm: sm}

	if _, err := d.Deploy(context.Background(), ServiceConfig{
		Name: "my-app", ImageRef: "img", AccessRoleArn: "arn:access", Port: "8080", CPU: "1024", Memory: "2048",
		Secrets: map[string]string{"DATABASE_URL": "postgres://x"},
	}); err != nil {
		t.Fatal(err)
	}

	img := ar.lastCreate.SourceConfiguration.ImageRepository.ImageConfiguration
	if img.RuntimeEnvironmentSecrets["DATABASE_URL"] != "arn:secret:prod/my-app/DATABASE_URL" {
		t.Errorf("RuntimeEnvironmentSecrets not wired: %v", img.RuntimeEnvironmentSecrets)
	}
	wantRole := "arn:aws:iam::123:role/prod-apprunner-instance-my-app"
	if aws.ToString(ar.lastCreate.InstanceConfiguration.InstanceRoleArn) != wantRole {
		t.Errorf("InstanceRoleArn = %q, want %q", aws.ToString(ar.lastCreate.InstanceConfiguration.InstanceRoleArn), wantRole)
	}
	if len(sm.created) != 1 {
		t.Errorf("expected the secret to be created, got %v", sm.created)
	}
	if !strings.Contains(iam.putPolicies["prod-apprunner-instance-my-app"], "arn:secret:prod/my-app/DATABASE_URL") {
		t.Errorf("instance role policy missing the secret ARN: %s", iam.putPolicies["prod-apprunner-instance-my-app"])
	}
}

// Regression for the shared-instance-role bug: deploying a second secret-bearing
// service must NOT strip the first service's secret access. Each service gets its
// own per-service instance role, so their inline policies don't overwrite.
func TestInstanceRolePerService(t *testing.T) {
	iam := &fakeIAM{}
	d := &Deployer{iam: iam, sm: &fakeSecrets{}}

	d.ar = &fakeAR{createArn: "arn:a"}
	if _, err := d.Deploy(context.Background(), ServiceConfig{
		Name: "svc-a", Port: "8080", CPU: "1024", Memory: "2048", Secrets: map[string]string{"A_SECRET": "x"},
	}); err != nil {
		t.Fatal(err)
	}
	d.ar = &fakeAR{createArn: "arn:b"} // second service reuses the shared IAM state
	if _, err := d.Deploy(context.Background(), ServiceConfig{
		Name: "svc-b", Port: "8080", CPU: "1024", Memory: "2048", Secrets: map[string]string{"B_SECRET": "y"},
	}); err != nil {
		t.Fatal(err)
	}

	aPolicy := iam.putPolicies["prod-apprunner-instance-svc-a"]
	bPolicy := iam.putPolicies["prod-apprunner-instance-svc-b"]
	if aPolicy == "" || bPolicy == "" {
		t.Fatalf("expected a distinct role+policy per service; got A=%q B=%q", aPolicy, bPolicy)
	}
	if !strings.Contains(aPolicy, "prod/svc-a/A_SECRET") {
		t.Errorf("service A lost its secret access after B deployed: %s", aPolicy)
	}
	if strings.Contains(aPolicy, "B_SECRET") {
		t.Errorf("service A policy leaked B's secret: %s", aPolicy)
	}
}

// The update (redeploy) path must also carry secrets + the instance role.
func TestDeployWithSecretsUpdatesExisting(t *testing.T) {
	ar := &fakeAR{existing: map[string]string{"my-app": "arn:existing"}}
	d := &Deployer{ar: ar, iam: &fakeIAM{}, sm: &fakeSecrets{}}

	if _, err := d.Deploy(context.Background(), ServiceConfig{
		Name: "my-app", ImageRef: "img2", AccessRoleArn: "arn:access", Port: "8080", CPU: "1024", Memory: "2048",
		Secrets: map[string]string{"DATABASE_URL": "postgres://x"},
	}); err != nil {
		t.Fatal(err)
	}
	if ar.lastUpdate == nil {
		t.Fatal("expected UpdateService on the existing service")
	}
	img := ar.lastUpdate.SourceConfiguration.ImageRepository.ImageConfiguration
	if img.RuntimeEnvironmentSecrets["DATABASE_URL"] != "arn:secret:prod/my-app/DATABASE_URL" {
		t.Errorf("update did not carry secrets: %v", img.RuntimeEnvironmentSecrets)
	}
	if aws.ToString(ar.lastUpdate.InstanceConfiguration.InstanceRoleArn) == "" {
		t.Error("update did not carry the instance role")
	}
}
