package provider

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

const testExternalSecretARN = "arn:aws:secretsmanager:us-east-1:123456789012:secret:example/key-AbCd12"
const testExternalRawKey = "sk-abcdefghijklmnopqrstuvwxyz0123456789"

// All identity and credential fixtures in these tests are synthetic.
type fakeExternalSecrets struct {
	token   string
	version string
	err     error
	output  *secretsmanager.GetSecretValueOutput
	calls   int
}

func (fake *fakeExternalSecrets) GetSecretValue(ctx context.Context, region string, input *secretsmanager.GetSecretValueInput) (*secretsmanager.GetSecretValueOutput, error) {
	fake.calls++
	if region != "us-east-1" || aws.ToString(input.SecretId) != testExternalSecretARN || aws.ToString(input.VersionStage) != "AWSCURRENT" || input.VersionId != nil {
		return nil, errors.New("incorrect synthetic AWS request")
	}
	if fake.err != nil {
		return nil, fake.err
	}
	if fake.output != nil {
		return fake.output, nil
	}
	return &secretsmanager.GetSecretValueOutput{
		ARN: aws.String(testExternalSecretARN), SecretString: aws.String(fake.token),
		VersionId: aws.String(fake.version), VersionStages: []string{"AWSCURRENT"},
	}, nil
}

func testExternalConfig() KeyResourceModel {
	return KeyResourceModel{
		ExternalRotation: types.ObjectValueMust(externalRotationAttributeTypes, map[string]attr.Value{"secret_arn": types.StringValue(testExternalSecretARN), "version_hash": types.StringNull()}),
		ServiceAccountID: types.StringValue("example-service-account"),
		TeamID:           types.StringValue("example-team"), ProjectID: types.StringValue("example-project"),
		KeyAlias: types.StringValue("example-alias"), MaxBudget: types.Float64Value(20),
		Models: types.ListValueMust(types.StringType, []attr.Value{types.StringValue("example-model")}),
	}
}

func TestExternalKeyARNValidation(t *testing.T) {
	for _, value := range []string{
		"example/key", "arn:aws:s3:us-east-1:123456789012:secret:example-AbCd12",
		"arn:aws:secretsmanager::123456789012:secret:example-AbCd12",
		"arn:aws:secretsmanager:us-east-1:123:secret:example-AbCd12",
		"arn:aws:secretsmanager:us-east-1:123456789012:secret:example",
		"arn:aws:secretsmanager:us-east-1:123456789012:secret:example-*",
	} {
		if _, err := parseExternalSecretARN(value); err == nil {
			t.Fatalf("accepted invalid ARN %q", value)
		}
	}
	if parsed, err := parseExternalSecretARN(testExternalSecretARN); err != nil || parsed.Region != "us-east-1" {
		t.Fatal("full ARN not accepted")
	}
}

func TestExternalKeySecretValidationAndSanitization(t *testing.T) {
	ctx := context.Background()
	valid := func() *secretsmanager.GetSecretValueOutput {
		return &secretsmanager.GetSecretValueOutput{
			ARN: aws.String(testExternalSecretARN), SecretString: aws.String(testExternalRawKey),
			VersionId: aws.String("example-version"), VersionStages: []string{"AWSCURRENT"},
		}
	}
	for _, test := range []struct {
		name   string
		mutate func(*secretsmanager.GetSecretValueOutput)
	}{
		{"wrong account", func(out *secretsmanager.GetSecretValueOutput) {
			out.ARN = aws.String(strings.Replace(testExternalSecretARN, "123456789012", "000000000000", 1))
		}},
		{"wrong secret", func(out *secretsmanager.GetSecretValueOutput) {
			out.ARN = aws.String(strings.Replace(testExternalSecretARN, "example/key", "another/key", 1))
		}},
		{"missing version", func(out *secretsmanager.GetSecretValueOutput) { out.VersionId = nil }},
		{"wrong stage", func(out *secretsmanager.GetSecretValueOutput) { out.VersionStages = []string{"AWSPREVIOUS"} }},
		{"missing value", func(out *secretsmanager.GetSecretValueOutput) { out.SecretString = nil }},
		{"json", func(out *secretsmanager.GetSecretValueOutput) {
			out.SecretString = aws.String(`{"key":"` + testExternalRawKey + `"}`)
		}},
		{"whitespace", func(out *secretsmanager.GetSecretValueOutput) {
			out.SecretString = aws.String(testExternalRawKey + "\n")
		}},
		{"hash", func(out *secretsmanager.GetSecretValueOutput) {
			out.SecretString = aws.String(hashKeyForID(testExternalRawKey))
		}},
		{"short", func(out *secretsmanager.GetSecretValueOutput) { out.SecretString = aws.String("sk-short") }},
		{"binary", func(out *secretsmanager.GetSecretValueOutput) { out.SecretBinary = []byte(testExternalRawKey) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			output := valid()
			test.mutate(output)
			r := &KeyResource{externalSecrets: &fakeExternalSecrets{output: output}}
			if _, err := r.resolveExternalKey(ctx, testExternalSecretARN); err == nil || strings.Contains(err.Error(), testExternalRawKey) {
				t.Fatal("invalid secret accepted or secret leaked")
			}
		})
	}
	r := &KeyResource{externalSecrets: &fakeExternalSecrets{err: errors.New("credential-secret " + testExternalRawKey)}}
	if _, err := r.resolveExternalKey(ctx, testExternalSecretARN); err == nil || strings.Contains(err.Error(), "credential-secret") || strings.Contains(err.Error(), testExternalRawKey) {
		t.Fatal("AWS error not sanitized")
	}
	r.externalSecrets = &fakeExternalSecrets{token: testExternalRawKey, version: "example-version"}
	if credential, err := r.resolveExternalKey(ctx, testExternalSecretARN); err != nil || credential.token != testExternalRawKey {
		t.Fatal("valid secret not accepted")
	}
}

func TestExternalKeyUnsupportedConfiguration(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*KeyResourceModel)
	}{
		{"key", func(data *KeyResourceModel) { data.Key = types.StringValue(testExternalRawKey) }},
		{"key_wo", func(data *KeyResourceModel) { data.KeyWO = types.StringValue(testExternalRawKey) }},
		{"key_wo_version", func(data *KeyResourceModel) { data.KeyWOVersion = types.StringValue("example") }},
		{"metadata_json", func(data *KeyResourceModel) { data.MetadataJSON = types.StringValue(`{}`) }},
		{"config_json", func(data *KeyResourceModel) { data.ConfigJSON = types.StringValue(`{}`) }},
		{"permissions_json", func(data *KeyResourceModel) { data.PermissionsJSON = types.StringValue(`{}`) }},
		{"unknown identity", func(data *KeyResourceModel) { data.ServiceAccountID = types.StringUnknown() }},
		{"empty identity", func(data *KeyResourceModel) { data.TeamID = types.StringValue("") }},
		{"missing project", func(data *KeyResourceModel) { data.ProjectID = types.StringNull() }},
		{"missing alias", func(data *KeyResourceModel) { data.KeyAlias = types.StringNull() }},
		{"blocked", func(data *KeyResourceModel) { data.Blocked = types.BoolValue(true) }},
		{"invitation", func(data *KeyResourceModel) { data.SendInviteEmail = types.BoolValue(true) }},
		{"metadata identity", func(data *KeyResourceModel) {
			data.Metadata = types.MapValueMust(types.StringType, map[string]attr.Value{"service_account_id": types.StringValue("wrong-account")})
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			data := testExternalConfig()
			test.mutate(&data)
			if _, err := externalKeyConfiguration(data, true); err == nil {
				t.Fatal("unsupported configuration accepted")
			}
		})
	}
}
