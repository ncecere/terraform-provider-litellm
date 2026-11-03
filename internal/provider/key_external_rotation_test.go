package provider

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
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

func TestExternalKeyClearAuthoritativeReadback(t *testing.T) {
	for _, test := range []struct {
		name          string
		clearTPD      bool
		clearRouter   bool
		tpd           interface{}
		router        interface{}
		omitTPD       bool
		omitRouter    bool
		unownedRemote bool
		wantError     bool
	}{
		{name: "tpd null", clearTPD: true},
		{name: "tpd ignored", clearTPD: true, tpd: 500, wantError: true},
		{name: "tpd omitted", clearTPD: true, omitTPD: true, wantError: true},
		{name: "tpd malformed", clearTPD: true, tpd: "response-secret", wantError: true},
		{name: "router empty object", clearRouter: true, router: map[string]interface{}{}},
		{name: "router null", clearRouter: true},
		{name: "router encoded empty object", clearRouter: true, router: "{}"},
		{name: "router ignored", clearRouter: true, router: map[string]interface{}{"num_retries": 3}, wantError: true},
		{name: "router omitted", clearRouter: true, omitRouter: true, wantError: true},
		{name: "router malformed", clearRouter: true, router: "response-secret", wantError: true},
		{name: "both cleared", clearTPD: true, clearRouter: true, router: map[string]interface{}{}},
		{name: "router ignored after tpd clear", clearTPD: true, clearRouter: true, router: map[string]interface{}{"num_retries": 3}, wantError: true},
		{name: "tpd ignored after router clear", clearTPD: true, clearRouter: true, tpd: 500, router: map[string]interface{}{}, wantError: true},
		{name: "unowned remote overrides remain unmanaged", unownedRemote: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			config := testExternalConfig()
			info := map[string]interface{}{
				"status": "active", "blocked": false, "team_id": "example-team", "project_id": "example-project", "key_alias": "example-alias",
				"metadata": map[string]interface{}{"service_account_id": "example-service-account"}, "max_budget": 20, "models": []string{"example-model"},
				"tpd_limit": 500, "router_settings": map[string]interface{}{"num_retries": 3},
			}
			priorConfig := config
			if test.clearTPD {
				priorConfig.TPDLimit = types.Int64Value(500)
			}
			if test.clearRouter {
				var err error
				priorConfig.RouterSettings, _, err = keyRouterSettingsFromAPI(info["router_settings"], types.ObjectNull(keyRouterSettingsAttrTypes))
				if err != nil {
					t.Fatal(err)
				}
			}
			aws := &fakeExternalSecrets{token: testExternalRawKey, version: "example-version"}
			reads, updates := 0, 0
			client := testRetryClient(func(request *http.Request) (*http.Response, error) {
				hash, _ := keyHashFromID(hashKeyForID(testExternalRawKey))
				var body interface{}
				switch {
				case request.Method == http.MethodGet && request.URL.Path == "/key/info":
					reads++
					if request.URL.Query().Get("key") != hash {
						t.Error("read did not use exact token hash")
					}
					body = map[string]interface{}{"key": hash, "info": info}
				case request.Method == http.MethodPost && request.URL.Path == "/key/update":
					updates++
					var payload map[string]interface{}
					if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
						t.Fatal(err)
					}
					wanted := map[string]interface{}{"key": hash, "models": []interface{}{"example-model"}, "max_budget": float64(20)}
					if test.clearTPD {
						wanted["tpd_limit"] = nil
						info["tpd_limit"] = test.tpd
						if test.omitTPD {
							delete(info, "tpd_limit")
						}
					}
					if test.clearRouter {
						wanted["router_settings"] = map[string]interface{}{}
						info["router_settings"] = test.router
						if test.omitRouter {
							delete(info, "router_settings")
						}
					}
					if test.unownedRemote {
						wanted["max_budget"] = float64(30)
						info["max_budget"] = 30
					}
					if !reflect.DeepEqual(payload, wanted) {
						t.Errorf("update payload changed: got %#v want %#v", payload, wanted)
					}
					body = map[string]interface{}{"key": hash}
				default:
					t.Fatalf("unexpected external endpoint: %s %s", request.Method, request.URL.Path)
				}
				encoded, err := json.Marshal(body)
				if err != nil {
					t.Fatal(err)
				}
				response := testHTTPResponse(request, http.StatusOK, nil)
				response.Body = io.NopCloser(strings.NewReader(string(encoded)))
				return response, nil
			})
			resource := &KeyResource{client: client, externalSecrets: aws}
			prior, _, _, err := resource.externalKeySnapshot(ctx, priorConfig)
			if err != nil {
				t.Fatal(err)
			}
			if test.unownedRemote {
				config.MaxBudget = types.Float64Value(30)
			}
			snapshot, _, _, err := resource.externalKeySnapshot(ctx, config)
			if err != nil {
				t.Fatal(err)
			}
			plan := externalKeyDesired(config, snapshot)
			before := prior
			reads, aws.calls = 0, 0
			next, adopted, err := resource.applyExternalKey(ctx, config, plan, &prior)
			if (err != nil) != test.wantError || !adopted {
				t.Fatalf("clear confirmation: adopted=%t err=%v", adopted, err)
			}
			if err != nil && (strings.Contains(err.Error(), testExternalRawKey) || strings.Contains(err.Error(), "response-secret")) {
				t.Fatal("readback values leaked")
			}
			if !reflect.DeepEqual(prior, before) {
				t.Fatal("prior state mutated")
			}
			if !test.wantError && !reflect.DeepEqual(next, plan) {
				t.Fatal("successful clear did not retain exact plan/ID")
			}
			if updates != 1 || reads != 2 || aws.calls != 2 {
				t.Fatalf("confirmation not bounded: updates=%d reads=%d secret reads=%d", updates, reads, aws.calls)
			}
		})
	}
}
