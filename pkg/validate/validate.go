package validate

import (
	"errors"
	"fmt"

	v1 "k8s.io/api/core/v1"

	deccov1beta2 "github.com/platform9/decco/api/v1beta2"
)

var (
	ErrInvalidSpaceSpec               = errors.New("SpaceSpec is invalid")
	ErrHttpCertSecretNameMissing      = errors.New("HttpCertSecretName is required")
	ErrTLSSecretHasInvalidType        = errors.New("type of the secret must be '" + string(v1.SecretTypeTLS) + "'")
	ErrTLSSecretMissingKey            = errors.New("TLS secret is missing the key.tls field")
	ErrTLSSecretMissingCert           = errors.New("TLS secret is missing the cert.tls field")
	ErrDomainNameMissing              = errors.New("missing domain name")
	ErrInvalidProjectName             = errors.New("invalid project name")
	ErrInvalidPort                    = errors.New("endpoint has invalid port value")
	ErrInvalidUrlPath                 = errors.New("invalid url path")
	ErrBothUrlPathAndDisableTcpVerify = errors.New("url path and disable tcp verify cannot both be set")
	ErrNoTcpCert                      = errors.New("space does not support TCP apps because cert info missing")
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

// TODO(erwin) remove the need for tcpCertAndCaSecretName
func AppSpec(app deccov1beta2.AppSpec, tcpCertAndCaSecretName string) error {
	for _, e := range app.Endpoints {
		if e.Port == 0 {
			return ErrInvalidPort
		}
		if e.HttpPath == "" && tcpCertAndCaSecretName == "" {
			return ErrNoTcpCert
		}
		if e.HttpPath != "" && e.DisableTcpClientTlsVerification {
			return ErrBothUrlPathAndDisableTcpVerify
		}
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
