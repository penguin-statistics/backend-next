package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"

	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"

	"exusiai.dev/backend-next/internal/app/appconfig"
)

type Crypto struct {
	privateKey *rsa.PrivateKey
	iv         []byte
}

func intSliceToByteSlice(iv []int) []byte {
	b := make([]byte, len(iv))
	for i, v := range iv {
		b[i] = byte(v)
	}
	return b
}

func NewCrypto(conf *appconfig.Config) *Crypto {
	pk := []byte("-----BEGIN RSA PRIVATE KEY-----\n")
	pk = append(pk, conf.RecognitionEncryptionPrivateKey...)
	pk = append(pk, []byte("\n-----END RSA PRIVATE KEY-----")...)

	rsaPrivateKeyPEMBlock, _ := pem.Decode(pk)
	rsaPrivateKeyNotAsserted, err := x509.ParsePKCS8PrivateKey(rsaPrivateKeyPEMBlock.Bytes)
	if err != nil {
		log.Warn().
			Err(err).
			Msg("failed to parse private key. This is not a fatal error, but recognition API will not work.")

		return &Crypto{}
	}
	rsaPrivateKey := rsaPrivateKeyNotAsserted.(*rsa.PrivateKey)

	iv := intSliceToByteSlice(conf.RecognitionEncryptionIV)

	return &Crypto{
		privateKey: rsaPrivateKey,
		iv:         iv,
	}
}

func pkcs7UnPadding(origData []byte) []byte {
	length := len(origData)
	unpadding := int(origData[length-1])
	return origData[:(length - unpadding)]
}

func (c *Crypto) Decrypt(aesKeyEncryptedBase64, bodyEncryptedBase64 string) ([]byte, error) {
	aesKeyRSAEncryptedBytes, err := base64.StdEncoding.DecodeString(aesKeyEncryptedBase64)
	if err != nil {
		return nil, err
	}

	aesKeyBase64, err := rsa.DecryptOAEP(sha256.New(), rand.Reader, c.privateKey, aesKeyRSAEncryptedBytes, nil)
	if err != nil {
		return nil, err
	}

	aesKeyBytes, err := base64.StdEncoding.DecodeString(string(aesKeyBase64))
	if err != nil {
		return nil, err
	}

	aesCipherBlock, err := aes.NewCipher(aesKeyBytes)
	if err != nil {
		return nil, err
	}

	bodyEncryptedBytes, err := base64.StdEncoding.DecodeString(bodyEncryptedBase64)
	if err != nil {
		return nil, err
	}

	aesCipherBlockSize := aesCipherBlock.BlockSize()

	if len(bodyEncryptedBytes)%aesCipherBlockSize != 0 {
		return nil, errors.New("ciphertext is not a multiple of the block size")
	}

	aesDecrypter := cipher.NewCBCDecrypter(aesCipherBlock, c.iv)

	aesDecrypter.CryptBlocks(bodyEncryptedBytes, bodyEncryptedBytes)
	// bodyEncryptedBytes is now the original plaintext.

	bodyEncryptedBytes = pkcs7UnPadding(bodyEncryptedBytes)

	return bodyEncryptedBytes, nil
}
