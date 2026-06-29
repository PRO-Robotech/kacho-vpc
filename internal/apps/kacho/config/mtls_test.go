// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package config_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/config"
)

// writeTestCert генерирует одноразовую self-signed PEM-тройку cert+key+CA для
// тестов mTLS-обвязки (без реального PKI). Возвращает пути cert, key, ca.
func writeTestCert(t *testing.T) (certFile, keyFile, caFile string) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "kacho-vpc-test"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		DNSNames:              []string{"kacho-iam.kacho.svc.cluster.local"},
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	require.NoError(t, err)
	dir := t.TempDir()
	certFile = filepath.Join(dir, "cert.pem")
	keyFile = filepath.Join(dir, "key.pem")
	caFile = filepath.Join(dir, "ca.pem")
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	require.NoError(t, os.WriteFile(certFile, certPEM, 0o600))
	require.NoError(t, os.WriteFile(caFile, certPEM, 0o600))
	keyDER, err := x509.MarshalECPrivateKey(priv)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600))
	return certFile, keyFile, caFile
}

// TestMTLS_SEC_D_16_DisabledDefaultInsecure — enable=false (default): dial/server-opts
// строятся insecure (backward-compat), cert-файлы не читаются.
func TestMTLS_SEC_D_16_DisabledDefaultInsecure(t *testing.T) {
	m, err := config.LoadMTLS()
	require.NoError(t, err)
	assert.False(t, m.IAMRegisterMTLS.Enable, "vpc→iam mTLS off by default")
	assert.False(t, m.GeoMTLS.Enable, "vpc→geo mTLS off by default")
	assert.False(t, m.PublicServerMTLS.Enable, "public server mTLS off by default")
	assert.False(t, m.InternalServerMTLS.Enable, "internal server mTLS off by default")

	// При disabled все ребра строят creds в insecure-режиме.
	for name, build := range map[string]func() error{
		"iam-register-client": func() error { _, e := m.IAMRegisterClientCreds(); return e },
		"geo-client":          func() error { _, e := m.GeoClientCreds(); return e },
		"public-server":       func() error { _, e := m.PublicServerCreds(); return e },
		"internal-server":     func() error { _, e := m.InternalServerCreds(); return e },
	} {
		require.NoError(t, build(), "%s: disabled edge must build insecure creds", name)
	}
}

// TestMTLS_SEC_I_01_DisabledDefaultInsecure — ребра vpc→iam read/authz
// (ProjectService.Get, InternalIAMService.Check + list-filter) по умолчанию выключены
// и строят insecure creds (нулевая dev-регрессия).
func TestMTLS_SEC_I_01_DisabledDefaultInsecure(t *testing.T) {
	m, err := config.LoadMTLS()
	require.NoError(t, err)
	assert.False(t, m.IAMProjectMTLS.Enable, "vpc→iam ProjectService.Get mTLS off by default")
	assert.False(t, m.IAMAuthzMTLS.Enable, "vpc→iam Check/list-filter mTLS off by default")

	for name, build := range map[string]func() error{
		"iam-project-client": func() error { _, e := m.IAMProjectClientCreds(); return e },
		"iam-authz-client":   func() error { _, e := m.IAMAuthzClientCreds(); return e },
	} {
		require.NoError(t, build(), "%s: disabled edge must build insecure creds", name)
	}
}

// TestMTLS_SEC_I_02_ProjectEdgeClientCredsBuild — enable=true с валидной тройкой
// и ServerName=kacho-iam (dial-host :9090) строит client-transport-creds для ребра
// ProjectService.Get (existence/leaf-owner).
func TestMTLS_SEC_I_02_ProjectEdgeClientCredsBuild(t *testing.T) {
	certFile, keyFile, caFile := writeTestCert(t)
	t.Setenv("KACHO_VPC_IAM_PROJECT_MTLS_ENABLE", "true")
	t.Setenv("KACHO_VPC_IAM_PROJECT_MTLS_CERTFILE", certFile)
	t.Setenv("KACHO_VPC_IAM_PROJECT_MTLS_KEYFILE", keyFile)
	t.Setenv("KACHO_VPC_IAM_PROJECT_MTLS_CAFILES", caFile)
	t.Setenv("KACHO_VPC_IAM_PROJECT_MTLS_SERVERNAME", "kacho-iam.kacho.svc.cluster.local")

	m, err := config.LoadMTLS()
	require.NoError(t, err)
	assert.True(t, m.IAMProjectMTLS.Enable)
	assert.Equal(t, "kacho-iam.kacho.svc.cluster.local", m.IAMProjectMTLS.ServerName)
	opt, err := m.IAMProjectClientCreds()
	require.NoError(t, err, "valid cert trio → client creds build")
	require.NotNil(t, opt)
}

// TestMTLS_SEC_I_03_AuthzEdgeClientCredsBuild — enable=true с валидной тройкой
// и ServerName=kacho-iam-internal (dial-host :9091) строит client-transport-creds для
// ребра InternalIAMService.Check (общего для per-RPC gate и list-filter).
func TestMTLS_SEC_I_03_AuthzEdgeClientCredsBuild(t *testing.T) {
	certFile, keyFile, caFile := writeTestCert(t)
	t.Setenv("KACHO_VPC_IAM_AUTHZ_MTLS_ENABLE", "true")
	t.Setenv("KACHO_VPC_IAM_AUTHZ_MTLS_CERTFILE", certFile)
	t.Setenv("KACHO_VPC_IAM_AUTHZ_MTLS_KEYFILE", keyFile)
	t.Setenv("KACHO_VPC_IAM_AUTHZ_MTLS_CAFILES", caFile)
	t.Setenv("KACHO_VPC_IAM_AUTHZ_MTLS_SERVERNAME", "kacho-iam-internal.kacho.svc.cluster.local")

	m, err := config.LoadMTLS()
	require.NoError(t, err)
	assert.True(t, m.IAMAuthzMTLS.Enable)
	assert.Equal(t, "kacho-iam-internal.kacho.svc.cluster.local", m.IAMAuthzMTLS.ServerName)
	opt, err := m.IAMAuthzClientCreds()
	require.NoError(t, err, "valid cert trio → client creds build")
	require.NotNil(t, opt)
}

// TestMTLS_SEC_I_06_FailClosed — каждое ребро vpc→iam read/authz с enable=true, но
// без CA/ServerName, fail-closed на этапе creds-build (без тихого insecure-fallback).
func TestMTLS_SEC_I_06_FailClosed(t *testing.T) {
	t.Run("project-missing-ca", func(t *testing.T) {
		t.Setenv("KACHO_VPC_IAM_PROJECT_MTLS_ENABLE", "true")
		m, err := config.LoadMTLS()
		require.NoError(t, err)
		_, err = m.IAMProjectClientCreds()
		require.Error(t, err, "enabled project edge without CA must fail-closed")
	})
	t.Run("authz-missing-ca", func(t *testing.T) {
		t.Setenv("KACHO_VPC_IAM_AUTHZ_MTLS_ENABLE", "true")
		m, err := config.LoadMTLS()
		require.NoError(t, err)
		_, err = m.IAMAuthzClientCreds()
		require.Error(t, err, "enabled authz edge without CA must fail-closed")
	})
}

// TestMTLS_SEC_D_17_EnabledClientCredsBuild — обвязка vpc→iam: enable=true с валидной
// тройкой cert/key/ca строит client-transport-creds (сам handshake покрыт bufconn-тестами corelib).
func TestMTLS_SEC_D_17_EnabledClientCredsBuild(t *testing.T) {
	certFile, keyFile, caFile := writeTestCert(t)
	t.Setenv("KACHO_VPC_IAM_REGISTER_MTLS_ENABLE", "true")
	t.Setenv("KACHO_VPC_IAM_REGISTER_MTLS_CERTFILE", certFile)
	t.Setenv("KACHO_VPC_IAM_REGISTER_MTLS_KEYFILE", keyFile)
	t.Setenv("KACHO_VPC_IAM_REGISTER_MTLS_CAFILES", caFile)
	t.Setenv("KACHO_VPC_IAM_REGISTER_MTLS_SERVERNAME", "kacho-iam.kacho.svc.cluster.local")

	m, err := config.LoadMTLS()
	require.NoError(t, err)
	assert.True(t, m.IAMRegisterMTLS.Enable)
	opt, err := m.IAMRegisterClientCreds()
	require.NoError(t, err, "valid cert trio → client creds build")
	require.NotNil(t, opt)
}

// TestMTLS_SEC_D_19_GeoEdgeClientCredsBuild — ребро vpc→geo (валидация zone_id):
// enable=true с валидной тройкой строит client-transport-creds.
func TestMTLS_SEC_D_19_GeoEdgeClientCredsBuild(t *testing.T) {
	certFile, keyFile, caFile := writeTestCert(t)
	t.Setenv("KACHO_VPC_GEO_MTLS_ENABLE", "true")
	t.Setenv("KACHO_VPC_GEO_MTLS_CERTFILE", certFile)
	t.Setenv("KACHO_VPC_GEO_MTLS_KEYFILE", keyFile)
	t.Setenv("KACHO_VPC_GEO_MTLS_CAFILES", caFile)
	t.Setenv("KACHO_VPC_GEO_MTLS_SERVERNAME", "kacho-geo.kacho.svc.cluster.local")

	m, err := config.LoadMTLS()
	require.NoError(t, err)
	assert.True(t, m.GeoMTLS.Enable)
	opt, err := m.GeoClientCreds()
	require.NoError(t, err)
	require.NotNil(t, opt)
}

// TestMTLS_SEC_D_20_ServerCredsRequireClientCA — server enable=true с cert/key +
// client-CA строит server-creds в режиме RequireAndVerifyClientCert (отказ при
// отсутствии client-cert энфорсится на handshake — в corelib).
func TestMTLS_SEC_D_20_ServerCredsRequireClientCA(t *testing.T) {
	certFile, keyFile, caFile := writeTestCert(t)
	t.Setenv("KACHO_VPC_INTERNAL_SERVER_MTLS_ENABLE", "true")
	t.Setenv("KACHO_VPC_INTERNAL_SERVER_MTLS_CERTFILE", certFile)
	t.Setenv("KACHO_VPC_INTERNAL_SERVER_MTLS_KEYFILE", keyFile)
	t.Setenv("KACHO_VPC_INTERNAL_SERVER_MTLS_CLIENTCAFILES", caFile)

	m, err := config.LoadMTLS()
	require.NoError(t, err)
	assert.True(t, m.InternalServerMTLS.Enable)
	opt, err := m.InternalServerCreds()
	require.NoError(t, err, "valid server cert + client CA → server creds build")
	require.NotNil(t, opt)
}

// TestMTLS_SEC_D_FailClosedMissingCA — enable=true, но пустой ca_files → ошибка
// (fail-closed, без тихого insecure-fallback).
func TestMTLS_SEC_D_FailClosedMissingCA(t *testing.T) {
	t.Setenv("KACHO_VPC_IAM_REGISTER_MTLS_ENABLE", "true")
	// Без CAFILES / SERVERNAME → fail-closed.
	m, err := config.LoadMTLS()
	require.NoError(t, err)
	_, err = m.IAMRegisterClientCreds()
	require.Error(t, err, "enabled mTLS without CA must fail-closed")
}
