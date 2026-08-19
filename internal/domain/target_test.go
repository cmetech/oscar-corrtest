package domain

import (
	"reflect"
	"strings"
	"testing"
)

func TestTargetInputCannotCarryCredentialValue(t *testing.T) {
	typeOfInput := reflect.TypeFor[TargetInput]()
	for i := range typeOfInput.NumField() {
		name := strings.ToLower(typeOfInput.Field(i).Name)
		if strings.Contains(name, "secret") || strings.Contains(name, "password") || strings.Contains(name, "token") {
			t.Fatalf("TargetInput exposes credential value field %q", typeOfInput.Field(i).Name)
		}
	}
}

func TestValidateTargetAcceptsTypedReferences(t *testing.T) {
	for _, credential := range []CredentialRef{
		{Kind: CredentialEnvironment, Reference: "OSCAR_API_TOKEN"},
		{Kind: CredentialFile, Reference: "/run/credentials/oscar"},
		{Kind: CredentialSystemd, Reference: "OSCAR_API_TOKEN"},
		{},
	} {
		target := TargetInput{
			DisplayName: "Lab A",
			BaseURL:     "https://oscar.example/api",
			Credential:  credential,
		}
		if err := target.Validate(); err != nil {
			t.Fatalf("credential=%+v error=%v", credential, err)
		}
	}
}

func TestValidateTargetRejectsUnsafeMetadata(t *testing.T) {
	tests := []TargetInput{
		{},
		{DisplayName: "lab", BaseURL: "ftp://oscar.example"},
		{DisplayName: "lab", BaseURL: "https://user:pass@oscar.example"},
		{DisplayName: "lab", BaseURL: "https://oscar.example?token=value"},
		{DisplayName: "lab", BaseURL: "https://oscar.example/#fragment"},
		{DisplayName: "lab", BaseURL: "https://oscar.example", Credential: CredentialRef{Kind: CredentialEnvironment, Reference: "bad-name"}},
		{DisplayName: "lab", BaseURL: "https://oscar.example", Credential: CredentialRef{Kind: CredentialFile, Reference: "relative"}},
		{DisplayName: "lab", BaseURL: "https://oscar.example", TLS: TLSPolicy{Insecure: true, CAPath: "/ca.pem"}},
	}
	for i, input := range tests {
		if err := input.Validate(); err == nil {
			t.Errorf("case %d accepted: %+v", i, input)
		}
	}
}
