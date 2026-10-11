package repository

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"math/big"
	"net"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestRedisSentinelTLSVerifiesEachEndpoint(t *testing.T) {
	for _, tc := range []struct {
		name, sentinelCertHost, masterCertHost string
		untrusted, wantErr                     bool
	}{
		{"different valid endpoint identities", "localhost", "127.0.0.1", false, false},
		{"wrong sentinel hostname", "wrong.example.invalid", "127.0.0.1", false, true},
		{"wrong master hostname", "localhost", "wrong.example.invalid", false, true},
		{"untrusted certificates", "localhost", "127.0.0.1", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			roots := x509.NewCertPool()
			master := miniredis.NewMiniRedis()
			require.NoError(t, master.StartTLS(redisEndpointTestCertificate(t, tc.masterCertHost, roots)))
			t.Cleanup(master.Close)
			sentinel, _ := syntheticRedisSentinel(t, master.Addr(), redisEndpointTestCertificate(t, tc.sentinelCertHost, roots))
			cfg := redisDiscoveryTestConfig()
			cfg.Redis.DiscoveryMode = config.RedisDiscoveryModeSentinel
			cfg.Redis.Username, cfg.Redis.Password = "", ""
			cfg.Redis.Host = "unused-direct.example.invalid"
			cfg.Redis.SentinelAddrs = []string{net.JoinHostPort("localhost", sentinel.Port())}
			cfg.Redis.SentinelMasterName = "cache-primary"
			cfg.Redis.EnableTLS = true
			opts := buildRedisFailoverOptions(cfg)
			opts.MaxRetries = -1
			if tc.untrusted {
				opts.TLSConfig.RootCAs = x509.NewCertPool()
			} else {
				opts.TLSConfig.RootCAs = roots
			}
			client := redis.NewFailoverClient(opts)
			t.Cleanup(func() { _ = client.Close() })
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			err := client.Ping(ctx).Err()
			if tc.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			require.Empty(t, opts.TLSConfig.ServerName, "dialing must not mutate the shared TLS configuration")
			require.False(t, opts.TLSConfig.InsecureSkipVerify)
		})
	}
}

func redisEndpointTestCertificate(t *testing.T, host string, roots *x509.CertPool) *tls.Config {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	cert := &x509.Certificate{
		SerialNumber: big.NewInt(1), NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	if ip := net.ParseIP(host); ip != nil {
		cert.IPAddresses = []net.IP{ip}
	} else {
		cert.DNSNames = []string{host}
	}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	require.NoError(t, err)
	parsed, err := x509.ParseCertificate(der)
	require.NoError(t, err)
	roots.AddCert(parsed)
	return &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}},
	}
}
