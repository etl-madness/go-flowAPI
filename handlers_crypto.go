package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/etl-madness/go-flow/pkg/flowcrypto"
)

type cryptoKeygenRequest struct {
	Algorithm    string `json:"algorithm"`     // "AES-256", "RSA-2048", "RSA-4096", "ECDSA-P256", "ECDSA-P384", "ED25519", "PGP"
	CommonName   string `json:"common_name"`   // For self-signed cert or PGP name
	Email        string `json:"email"`         // For PGP
	ValidityDays int    `json:"validity_days"` // For self-signed cert (default 365)
	GenerateCert bool   `json:"generate_cert"` // Generate self-signed X.509 code-signing cert
	ByteLength   int    `json:"byte_length"`   // For AES secure key (default 32)
}

type cryptoKeygenResponse struct {
	Algorithm      string `json:"algorithm"`
	SecureKey      string `json:"secure_key,omitempty"`
	PrivateKeyPEM  string `json:"private_key_pem,omitempty"`
	PublicKeyPEM   string `json:"public_key_pem,omitempty"`
	CertificatePEM string `json:"certificate_pem,omitempty"`
	Fingerprint    string `json:"fingerprint,omitempty"`
}

type cryptoEncryptDecryptRequest struct {
	Content   string `json:"content"`
	SecureKey string `json:"secure_key"`
}

type cryptoSignRequest struct {
	Content       string `json:"content"`
	Scheme        string `json:"scheme"`          // "OPENSSL", "PKCS7", "PGP"
	PrivateKeyPEM string `json:"private_key_pem"` // PEM or PGP armored private key
	CertPEM       string `json:"cert_pem"`        // Required for PKCS7, optional for OPENSSL
	Passphrase    string `json:"passphrase"`      // Optional PGP passphrase
	Wrap          bool   `json:"wrap"`            // Wrap into FLOWSIGNED:v1:<format>:<sig>:<content>
	EncryptMode   string `json:"encrypt_mode"`    // "none", "sign-then-encrypt", "encrypt-then-sign"
	SecureKey     string `json:"secure_key"`      // Used when EncryptMode != "none"
}

type cryptoSignResponse struct {
	Format            string `json:"format"`
	DetachedSignature string `json:"detached_signature"`
	SignedPayload     string `json:"signed_payload"`
	Encrypted         bool   `json:"encrypted"`
	Signed            bool   `json:"signed"`
}

type cryptoVerifyRequest struct {
	Content      string `json:"content"`
	Signature    string `json:"signature"`
	PublicKeyPEM string `json:"public_key_pem"`
	CertPEM      string `json:"cert_pem"`
	CACertPEM    string `json:"ca_cert_pem"`
	KeyringPEM   string `json:"keyring_pem"`
	SecureKey    string `json:"secure_key"`
}

type cryptoVerifyResponse struct {
	Valid            bool   `json:"valid"`
	Format           string `json:"format"`
	Algorithm        string `json:"algorithm"`
	SignerInfo       string `json:"signer_info"`
	Subject          string `json:"subject,omitempty"`
	Issuer           string `json:"issuer,omitempty"`
	SerialNumber     string `json:"serial_number,omitempty"`
	NotBefore        string `json:"not_before,omitempty"`
	NotAfter         string `json:"not_after,omitempty"`
	Fingerprint      string `json:"fingerprint,omitempty"`
	Encrypted        bool   `json:"encrypted"`
	Signed           bool   `json:"signed"`
	UnwrappedContent string `json:"unwrapped_content,omitempty"`
	ErrorMessage     string `json:"error_message,omitempty"`
}

func (s *APIServer) handleCryptoRouter(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "Method not allowed; use POST")
		return
	}

	action := strings.ToLower(strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v1/crypto/"), "/"))
	switch action {
	case "keygen":
		s.handleCryptoKeygen(w, r)
	case "encrypt":
		s.handleCryptoEncrypt(w, r)
	case "decrypt":
		s.handleCryptoDecrypt(w, r)
	case "sign":
		s.handleCryptoSign(w, r)
	case "verify":
		s.handleCryptoVerify(w, r)
	default:
		writeJSONError(w, http.StatusNotFound, fmt.Sprintf("unknown crypto endpoint %q (expected keygen, encrypt, decrypt, sign, or verify)", action))
	}
}

func (s *APIServer) handleCryptoKeygen(w http.ResponseWriter, r *http.Request) {
	var req cryptoKeygenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("invalid JSON body: %v", err))
		return
	}

	alg := strings.ToUpper(strings.TrimSpace(req.Algorithm))
	if alg == "" {
		alg = "RSA-2048"
	}

	switch alg {
	case "AES", "AES-256", "AES-256-GCM", "SECURE-KEY":
		secKey, err := flowcrypto.GenerateSecureKey(req.ByteLength)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, cryptoKeygenResponse{
			Algorithm: "AES-256-GCM",
			SecureKey: secKey,
		})
		return

	case "PGP", "OPENPGP", "GPG":
		cn := strings.TrimSpace(req.CommonName)
		if cn == "" {
			cn = "Flow Pipeline Signer"
		}
		email := strings.TrimSpace(req.Email)
		if email == "" {
			email = "signer@flow.local"
		}
		priv, pub, err := flowcrypto.GenerateOpenPGPKeyPair(cn, "Flow Digital Signer", email)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		sum := sha256.Sum256(pub)
		writeJSON(w, http.StatusOK, cryptoKeygenResponse{
			Algorithm:     "PGP",
			PrivateKeyPEM: string(priv),
			PublicKeyPEM:  string(pub),
			Fingerprint:   strings.ToUpper(hex.EncodeToString(sum[:16])),
		})
		return
	}

	var privPEM, pubPEM []byte
	var err error
	switch alg {
	case "RSA-4096":
		privPEM, pubPEM, err = flowcrypto.GenerateRSAKeyPair(4096)
	case "ECDSA-P256", "EC-P256", "P256":
		alg = "ECDSA-P256"
		privPEM, pubPEM, err = flowcrypto.GenerateECDSAKeyPair("P256")
	case "ECDSA-P384", "EC-P384", "P384":
		alg = "ECDSA-P384"
		privPEM, pubPEM, err = flowcrypto.GenerateECDSAKeyPair("P384")
	case "ED25519":
		privPEM, pubPEM, err = flowcrypto.GenerateEd25519KeyPair()
	default:
		alg = "RSA-2048"
		privPEM, pubPEM, err = flowcrypto.GenerateRSAKeyPair(2048)
	}
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	var certPEM []byte
	if req.GenerateCert {
		privKey, err := flowcrypto.ParsePrivateKeyPEM(privPEM)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		pubKey, _, err := flowcrypto.ParsePublicKeyOrCertPEM(pubPEM)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		certPEM, err = flowcrypto.GenerateSelfSignedCertificate(privKey, pubKey, req.CommonName, req.ValidityDays)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}

	sum := sha256.Sum256(pubPEM)
	if len(certPEM) > 0 {
		sum = sha256.Sum256(certPEM)
	}

	writeJSON(w, http.StatusOK, cryptoKeygenResponse{
		Algorithm:      alg,
		PrivateKeyPEM:  string(privPEM),
		PublicKeyPEM:   string(pubPEM),
		CertificatePEM: string(certPEM),
		Fingerprint:    strings.ToUpper(hex.EncodeToString(sum[:16])),
	})
}

func (s *APIServer) handleCryptoEncrypt(w http.ResponseWriter, r *http.Request) {
	var req cryptoEncryptDecryptRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("invalid JSON body: %v", err))
		return
	}
	key := strings.TrimSpace(req.SecureKey)
	if key == "" {
		key = strings.TrimSpace(r.Header.Get("X-Flow-Secure-Key"))
		if key == "" {
			key = s.cfg.DefaultSecureKey
		}
	}
	if key == "" {
		writeJSONError(w, http.StatusBadRequest, "secure_key is required for encryption")
		return
	}

	armored, err := flowcrypto.EncryptArmored([]byte(req.Content), key)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"content":   armored,
		"encrypted": true,
	})
}

func (s *APIServer) handleCryptoDecrypt(w http.ResponseWriter, r *http.Request) {
	var req cryptoEncryptDecryptRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("invalid JSON body: %v", err))
		return
	}
	key := strings.TrimSpace(req.SecureKey)
	if key == "" {
		key = strings.TrimSpace(r.Header.Get("X-Flow-Secure-Key"))
		if key == "" {
			key = s.cfg.DefaultSecureKey
		}
	}
	if key == "" {
		writeJSONError(w, http.StatusBadRequest, "secure_key is required for decryption")
		return
	}

	plain, err := flowcrypto.DecryptAuto([]byte(req.Content), key, true)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	enc, signed, sigFmt := inspectArtifactSecurity(string(plain))
	writeJSON(w, http.StatusOK, map[string]any{
		"content":          string(plain),
		"encrypted":        enc,
		"signed":           signed,
		"signature_format": sigFmt,
	})
}

func (s *APIServer) handleCryptoSign(w http.ResponseWriter, r *http.Request) {
	var req cryptoSignRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("invalid JSON body: %v", err))
		return
	}
	if strings.TrimSpace(req.Content) == "" {
		writeJSONError(w, http.StatusBadRequest, "content is required for signing")
		return
	}
	privBytes, err := loadInlineOrFileBytes(req.PrivateKeyPEM)
	if err != nil || len(privBytes) == 0 {
		writeJSONError(w, http.StatusBadRequest, "private_key_pem is required for signing")
		return
	}

	secKey := strings.TrimSpace(req.SecureKey)
	if secKey == "" {
		secKey = strings.TrimSpace(r.Header.Get("X-Flow-Secure-Key"))
		if secKey == "" {
			secKey = s.cfg.DefaultSecureKey
		}
	}

	encMode := strings.ToLower(strings.TrimSpace(req.EncryptMode))
	dataToSign := []byte(req.Content)

	// If Encrypt-then-Sign, encrypt the inner content before signing
	if encMode == "encrypt-then-sign" && !flowcrypto.IsEncryptedPayload(dataToSign) {
		if secKey == "" {
			writeJSONError(w, http.StatusBadRequest, "secure_key is required when encrypt_mode is encrypt-then-sign")
			return
		}
		armored, err := flowcrypto.EncryptArmored(dataToSign, secKey)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("failed encrypting content prior to signing: %v", err))
			return
		}
		dataToSign = []byte(armored)
	}

	scheme := strings.ToUpper(strings.TrimSpace(req.Scheme))
	if scheme == "" {
		scheme = "OPENSSL"
	}

	var sigBytes []byte
	var sigFormat string

	switch scheme {
	case "PGP", "OPENPGP", "GPG":
		sigBytes, err = flowcrypto.SignOpenPGP(dataToSign, privBytes, req.Passphrase)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		sigFormat = "PGP"

	case "PKCS7", "CMS", "WINDOWS":
		certBytes, certErr := loadInlineOrFileBytes(req.CertPEM)
		if certErr != nil || len(certBytes) == 0 {
			writeJSONError(w, http.StatusBadRequest, "cert_pem (X.509 certificate) is required for PKCS7 signing")
			return
		}
		sigBytes, err = flowcrypto.SignPKCS7Detached(dataToSign, certBytes, privBytes)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		sigFormat = "PKCS7"

	default:
		var keyType string
		sigBytes, keyType, err = flowcrypto.SignOpenSSL(dataToSign, privBytes)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		sigFormat = "OPENSSL-" + keyType
	}

	detachedEnv := flowcrypto.WrapSignatureOnly(sigFormat, sigBytes)
	wrappedPayload := flowcrypto.WrapSignedPayload(sigFormat, sigBytes, dataToSign)
	finalPayload := wrappedPayload
	if !req.Wrap {
		finalPayload = string(dataToSign)
	}

	isEncrypted := encMode == "encrypt-then-sign" || flowcrypto.IsEncryptedPayload(dataToSign)
	if encMode == "sign-then-encrypt" {
		if secKey == "" {
			writeJSONError(w, http.StatusBadRequest, "secure_key is required when encrypt_mode is sign-then-encrypt")
			return
		}
		encOuter, err := flowcrypto.EncryptArmored([]byte(wrappedPayload), secKey)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("failed encrypting signed payload: %v", err))
			return
		}
		finalPayload = encOuter
		isEncrypted = true
	}

	writeJSON(w, http.StatusOK, cryptoSignResponse{
		Format:            sigFormat,
		DetachedSignature: detachedEnv,
		SignedPayload:     finalPayload,
		Encrypted:         isEncrypted,
		Signed:            true,
	})
}

func (s *APIServer) handleCryptoVerify(w http.ResponseWriter, r *http.Request) {
	var req cryptoVerifyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("invalid JSON body: %v", err))
		return
	}

	rawBytes := []byte(strings.TrimSpace(req.Content))
	if len(rawBytes) == 0 {
		writeJSONError(w, http.StatusBadRequest, "content is required for verification")
		return
	}

	secKey := strings.TrimSpace(req.SecureKey)
	if secKey == "" {
		secKey = strings.TrimSpace(r.Header.Get("X-Flow-Secure-Key"))
		if secKey == "" {
			secKey = s.cfg.DefaultSecureKey
		}
	}

	wasEncrypted := flowcrypto.IsEncryptedPayload(rawBytes)

	// If outer layer is encrypted (Sign-then-Encrypt), decrypt first if key is available
	if wasEncrypted && secKey != "" {
		plain, err := flowcrypto.DecryptAuto(rawBytes, secKey, true)
		if err != nil {
			writeJSON(w, http.StatusOK, cryptoVerifyResponse{
				Valid:        false,
				Encrypted:    true,
				ErrorMessage: fmt.Sprintf("failed decrypting outer FLOWENC envelope: %v", err),
			})
			return
		}
		rawBytes = plain
	}

	var keyOrCertBytes, caCertBytes []byte
	if req.PublicKeyPEM != "" {
		keyOrCertBytes, _ = loadInlineOrFileBytes(req.PublicKeyPEM)
	} else if req.CertPEM != "" {
		keyOrCertBytes, _ = loadInlineOrFileBytes(req.CertPEM)
	} else if req.KeyringPEM != "" {
		keyOrCertBytes, _ = loadInlineOrFileBytes(req.KeyringPEM)
	}
	if req.CACertPEM != "" {
		caCertBytes, _ = loadInlineOrFileBytes(req.CACertPEM)
	}

	var verRes *flowcrypto.VerificationResult
	var unwrapped []byte
	var verifyErr error

	if flowcrypto.IsSignedPayload(rawBytes) {
		fmtStr, _, inner, parseErr := flowcrypto.ParseSignedPayload(rawBytes)
		if parseErr != nil {
			writeJSON(w, http.StatusOK, cryptoVerifyResponse{
				Valid:        false,
				Signed:       true,
				ErrorMessage: parseErr.Error(),
			})
			return
		}
		unwrapped = inner
		if flowcrypto.IsEncryptedPayload(unwrapped) {
			wasEncrypted = true
			if secKey != "" {
				if plainInner, decErr := flowcrypto.DecryptAuto(unwrapped, secKey, true); decErr == nil {
					unwrapped = plainInner
				}
			}
		}
		if len(keyOrCertBytes) == 0 && len(caCertBytes) == 0 {
			writeJSON(w, http.StatusOK, cryptoVerifyResponse{
				Valid:            false,
				Format:           fmtStr,
				Encrypted:        wasEncrypted,
				Signed:           true,
				UnwrappedContent: string(unwrapped),
				ErrorMessage:     "Signed envelope unwrapped, but no public_key_pem, cert_pem, or keyring_pem was provided to verify the cryptographic signature.",
			})
			return
		}
		var verifiedInner []byte
		verifiedInner, verRes, verifyErr = flowcrypto.VerifyPayloadAutoWithCA(rawBytes, keyOrCertBytes, caCertBytes)
		if verifyErr == nil && len(verifiedInner) > 0 {
			if flowcrypto.IsEncryptedPayload(verifiedInner) && secKey != "" {
				if plainInner, decErr := flowcrypto.DecryptAuto(verifiedInner, secKey, true); decErr == nil {
					verifiedInner = plainInner
				}
			}
			unwrapped = verifiedInner
		}
	} else if strings.TrimSpace(req.Signature) != "" {
		sigBytes, _ := loadInlineOrFileBytes(req.Signature)
		verRes, verifyErr = flowcrypto.VerifyAutoWithCA(rawBytes, sigBytes, keyOrCertBytes, caCertBytes)
		unwrapped = rawBytes
	} else {
		writeJSON(w, http.StatusOK, cryptoVerifyResponse{
			Valid:            false,
			Encrypted:        wasEncrypted,
			Signed:           false,
			UnwrappedContent: string(rawBytes),
			ErrorMessage:     "Payload is not wrapped in FLOWSIGNED:v1: and no detached signature was provided.",
		})
		return
	}

	if verifyErr != nil || verRes == nil {
		errMsg := "verification failed"
		if verifyErr != nil {
			errMsg = verifyErr.Error()
		}
		writeJSON(w, http.StatusOK, cryptoVerifyResponse{
			Valid:            false,
			Encrypted:        wasEncrypted,
			Signed:           true,
			UnwrappedContent: string(unwrapped),
			ErrorMessage:     errMsg,
		})
		return
	}

	resp := cryptoVerifyResponse{
		Valid:            verRes.Valid,
		Format:           verRes.Format,
		Algorithm:        verRes.Algorithm,
		SignerInfo:       verRes.SignerInfo,
		Encrypted:        wasEncrypted,
		Signed:           true,
		UnwrappedContent: string(unwrapped),
	}
	if verRes.Certificate != nil {
		resp.Subject = verRes.Certificate.Subject.String()
		resp.Issuer = verRes.Certificate.Issuer.String()
		resp.SerialNumber = verRes.Certificate.SerialNumber.String()
		resp.NotBefore = verRes.Certificate.NotBefore.UTC().Format(time.RFC3339)
		resp.NotAfter = verRes.Certificate.NotAfter.UTC().Format(time.RFC3339)
		sum := sha256.Sum256(verRes.Certificate.Raw)
		resp.Fingerprint = strings.ToUpper(hex.EncodeToString(sum[:16]))
	}
	writeJSON(w, http.StatusOK, resp)
}
