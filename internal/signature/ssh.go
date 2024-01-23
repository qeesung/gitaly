package signature

import (
	"crypto/rand"
	"crypto/sha512"
	"encoding/pem"
	"fmt"
	"time"

	"golang.org/x/crypto/ssh"
)

// SSHSigningKey is a struct that implements SigningKey interface for SSH keys
type SSHSigningKey struct {
	PrivateKey ssh.Signer
}

// sshSignature is a format for encoding a commit signature based on:
// https://github.com/openssh/openssh-portable/blob/V_9_2_P1/PROTOCOL.sshsig#37
type sshSignature struct {
	MagicHeader   [6]byte
	Version       uint32
	PublicKey     []byte
	Namespace     string
	Reserved      string
	HashAlgorithm string
	Signature     []byte
}

// signedData is a format for the data signature of which goes into sshSignature blob based on:
// https://github.com/openssh/openssh-portable/blob/V_9_2_P1/PROTOCOL.sshsig#L79
type signedData struct {
	MagicHeader   [6]byte
	Namespace     string
	Reserved      string
	HashAlgorithm string
	Hash          []byte
}

// The byte representation of "SSHSIG" string
var magicHeader = [6]byte{'S', 'S', 'H', 'S', 'I', 'G'}

const (
	version          = 1
	namespace        = "git"
	hashAlgorithm    = "sha512"
	sshSignatureType = "SSH SIGNATURE"
)

func parseSSHSigningKey(key []byte) (*SSHSigningKey, error) {
	privateKey, err := ssh.ParsePrivateKey(key)
	if err != nil {
		return nil, fmt.Errorf("parse private key: %w", err)
	}

	return &SSHSigningKey{PrivateKey: privateKey}, nil
}

// CreateSignature creates an SSH signature
func (sk *SSHSigningKey) CreateSignature(contentToSign []byte, _ time.Time) ([]byte, error) {
	signer, ok := sk.PrivateKey.(ssh.AlgorithmSigner)
	if !ok {
		return nil, fmt.Errorf("wrong type of the private key")
	}

	h := sha512.New()
	if _, err := h.Write(contentToSign); err != nil {
		return nil, fmt.Errorf("failed to create sha for content to sign: %w", err)
	}

	signedData := signedData{
		MagicHeader:   magicHeader,
		Namespace:     namespace,
		HashAlgorithm: hashAlgorithm,
		Hash:          h.Sum(nil),
	}

	// Mimic behavior from ssh-keygen:
	// https://github.com/openssh/openssh-portable/blob/V_9_3/sshsig.c#L183
	algorithm := signer.PublicKey().Type()
	if algorithm == ssh.KeyAlgoRSA {
		algorithm = ssh.KeyAlgoRSASHA512
	}

	signature, err := signer.SignWithAlgorithm(rand.Reader, ssh.Marshal(signedData), algorithm)
	if err != nil {
		return nil, fmt.Errorf("sign commit: %w", err)
	}

	sshSignature := sshSignature{
		MagicHeader:   magicHeader,
		Version:       version,
		PublicKey:     signer.PublicKey().Marshal(),
		Namespace:     namespace,
		HashAlgorithm: hashAlgorithm,
		Signature:     ssh.Marshal(signature),
	}

	armoredSignature := pem.EncodeToMemory(&pem.Block{
		Type:  sshSignatureType,
		Bytes: ssh.Marshal(sshSignature),
	})

	return armoredSignature, nil
}

// Verify method verifies whether a signature has been created by this signing key
func (sk *SSHSigningKey) Verify(signatureText, signedText []byte) error {
	block, rest := pem.Decode(signatureText)
	if block == nil || len(rest) > 0 || block.Type != sshSignatureType {
		return fmt.Errorf("invalid signature text")
	}

	sshSig := &sshSignature{}
	if err := ssh.Unmarshal(block.Bytes, sshSig); err != nil {
		return fmt.Errorf("parse signature text: %w", err)
	}

	signature := &ssh.Signature{}
	if err := ssh.Unmarshal(sshSig.Signature, signature); err != nil {
		return fmt.Errorf("parse signature: %w", err)
	}

	h := sha512.New()
	if _, err := h.Write(signedText); err != nil {
		return fmt.Errorf("failed to create sha for verifying content: %w", err)
	}

	signedData := signedData{
		MagicHeader:   sshSig.MagicHeader,
		Namespace:     sshSig.Namespace,
		HashAlgorithm: sshSig.HashAlgorithm,
		Hash:          h.Sum(nil),
	}

	return sk.PrivateKey.PublicKey().Verify(ssh.Marshal(signedData), signature)
}
