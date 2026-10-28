package provider

import (
	"context"
	"errors"
	"regexp"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/arn"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
)

// externalSecretResolver permits deterministic AWS responses without changing
// gateway authentication or the production AWS default credential chain.
type externalSecretResolver interface {
	GetSecretValue(context.Context, string, *secretsmanager.GetSecretValueInput) (*secretsmanager.GetSecretValueOutput, error)
}

type defaultExternalSecretResolver struct{}

func (defaultExternalSecretResolver) GetSecretValue(ctx context.Context, region string, input *secretsmanager.GetSecretValueInput) (*secretsmanager.GetSecretValueOutput, error) {
	// No provider-specific credentials, profile overrides, or endpoint overrides.
	cfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(region))
	if err != nil {
		return nil, err
	}
	return secretsmanager.NewFromConfig(cfg).GetSecretValue(ctx, input)
}

var (
	externalSecretAccountPattern  = regexp.MustCompile(`^[0-9]{12}$`)
	externalSecretRegionPattern   = regexp.MustCompile(`^[a-z]{2}(-[a-z0-9]+)+-[0-9]+$`)
	externalSecretResourcePattern = regexp.MustCompile(`^secret:[A-Za-z0-9/_+=.@-]+-[A-Za-z0-9]{6}$`)
	// LiteLLM 1.106.4 requires raw virtual keys to start with sk-. Accept
	// unquoted ASCII token strings only, never JSON, a hash, or whitespace.
	externalRawKeyPattern  = regexp.MustCompile(`^sk-[A-Za-z0-9_-]{13,}$`)
	errExternalSecretARN   = errors.New("external_rotation.secret_arn must be a full Secrets Manager secret ARN, including its six-character suffix, region, and twelve-digit account ID")
	errExternalSecretRead  = errors.New("the external AWSCURRENT secret could not be read; AWS credential, response, and transport details were omitted")
	errExternalSecretValue = errors.New("the external secret response must match the requested ARN and account, contain a nonempty version labeled AWSCURRENT, and contain only a raw sk- token of at least sixteen characters in SecretString; response values were omitted")
)

func parseExternalSecretARN(value string) (arn.ARN, error) {
	parsed, err := arn.Parse(value)
	if err != nil || parsed.Service != "secretsmanager" || !externalSecretAccountPattern.MatchString(parsed.AccountID) || !externalSecretRegionPattern.MatchString(parsed.Region) || !externalSecretResourcePattern.MatchString(parsed.Resource) {
		return arn.ARN{}, errExternalSecretARN
	}
	switch parsed.Partition {
	case "aws", "aws-cn", "aws-us-gov", "aws-iso", "aws-iso-b", "aws-iso-e", "aws-iso-f":
		return parsed, nil
	default:
		return arn.ARN{}, errExternalSecretARN
	}
}

type externalKeyCredential struct {
	token   string
	version string
}

func (r *KeyResource) resolveExternalKey(ctx context.Context, secretARN string) (externalKeyCredential, error) {
	requested, err := parseExternalSecretARN(secretARN)
	if err != nil {
		return externalKeyCredential{}, err
	}
	resolver := r.externalSecrets
	if resolver == nil {
		resolver = defaultExternalSecretResolver{}
	}
	response, err := resolver.GetSecretValue(ctx, requested.Region, &secretsmanager.GetSecretValueInput{
		SecretId: aws.String(secretARN), VersionStage: aws.String("AWSCURRENT"),
	})
	if err != nil {
		// SDK errors can include request identifiers, URLs, or credential values.
		return externalKeyCredential{}, errExternalSecretRead
	}
	if response == nil {
		return externalKeyCredential{}, errExternalSecretValue
	}
	returned, err := parseExternalSecretARN(aws.ToString(response.ARN))
	current := false
	for _, stage := range response.VersionStages {
		current = current || stage == "AWSCURRENT"
	}
	if err != nil || returned.AccountID != requested.AccountID || aws.ToString(response.ARN) != secretARN || !current || aws.ToString(response.VersionId) == "" || len(response.SecretBinary) != 0 || !externalRawKeyPattern.MatchString(aws.ToString(response.SecretString)) {
		return externalKeyCredential{}, errExternalSecretValue
	}
	return externalKeyCredential{token: aws.ToString(response.SecretString), version: aws.ToString(response.VersionId)}, nil
}
