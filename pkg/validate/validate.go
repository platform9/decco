package validate

import (
	"errors"
	"fmt"

	v1 "k8s.io/api/core/v1"

	deccov1beta2 "github.com/platform9/decco/api/v1beta2"
)

var (
	ErrInvalidSpaceSpec          = errors.New("SpaceSpec is invalid")
	ErrHttpCertSecretNameMissing = errors.New("HttpCertSecretName is required")
	ErrTLSSecretHasInvalidType   = errors.New("type of the secret must be '" + string(v1.SecretTypeTLS) + "'")
	ErrTLSSecretMissingKey       = errors.New("TLS secret is missing the key.tls field")
	ErrTLSSecretMissingCert      = errors.New("TLS secret is missing the cert.tls field")
	ErrDomainNameMissing         = errors.New("missing domain name")
	ErrInvalidProjectName        = errors.New("invalid project name")
)

func SpaceSpec(spec deccov1beta2.SpaceSpec) error {
	if spec.HttpCertSecretName == "" {
		return fmt.Errorf("%s: %w", ErrInvalidSpaceSpec, ErrHttpCertSecretNameMissing)
	}

	if spec.DomainName == "" {
		return fmt.Errorf("%s: %w", ErrInvalidSpaceSpec, ErrDomainNameMissing)
	}

	if spec.Project == deccov1beta2.ReservedProjectName {
		return fmt.Errorf("%s: %w", ErrInvalidSpaceSpec, ErrInvalidProjectName)
	}

	return nil
}

func TLSSecret(secret *v1.Secret) error {
	if secret.Type != v1.SecretTypeTLS {
		return ErrTLSSecretHasInvalidType
	}

	if !hasNonEmptyField(secret, "tls.crt") {
		return ErrTLSSecretMissingKey
	}

	if !hasNonEmptyField(secret, "tls.key") {
		return ErrTLSSecretMissingCert
	}

	return nil
}

func hasNonEmptyField(secret *v1.Secret, key string) bool {
	if secret.Data != nil {
		d, ok := secret.Data[key]
		if ok && len(d) > 0 {
			return true
		}
	}
	if secret.StringData != nil {
		s, ok := secret.StringData[key]
		if ok && len(s) > 0 {
			return true
		}
	}
	return false
}
