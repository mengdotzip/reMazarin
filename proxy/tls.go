package proxy

import (
	"crypto/tls"
	"crypto/x509"
	"log/slog"
	"time"

	"github.com/mdobak/go-xerrors"
)

func createTLSConfig(certPath, keyPath string) (*tls.Config, error) {
	cert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return nil, xerrors.Newf("load TLS certificate: %w", err)
	}

	if len(cert.Certificate) > 0 {
		parsedCert, err := x509.ParseCertificate(cert.Certificate[0])
		if err == nil {
			daysUntilExpiry := time.Until(parsedCert.NotAfter).Hours() / 24

			if daysUntilExpiry < 7 {
				slog.Warn("certificate expires soon",
					"days_remaining", int(daysUntilExpiry),
					"expiry_date", parsedCert.NotAfter,
				)
			} else {
				slog.Info("certificate loaded",
					"days_until_expiry", int(daysUntilExpiry),
					"not_after", parsedCert.NotAfter,
				)
			}
		}
	}

	cfg := &tls.Config{
		Certificates:             []tls.Certificate{cert},
		PreferServerCipherSuites: true,

		CurvePreferences: []tls.CurveID{
			tls.X25519,
			tls.CurveP256,
			tls.X25519MLKEM768,
		},

		MinVersion: tls.VersionTLS12,

		CipherSuites: []uint16{
			tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305,
			tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305,
			tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
		},
	}

	return cfg, nil
}
