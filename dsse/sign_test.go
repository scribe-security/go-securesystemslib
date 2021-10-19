package dsse

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"math/big"
	"testing"

	"github.com/codahale/rfc6979"
	"github.com/stretchr/testify/assert"
)

var errLength = errors.New("invalid length")

func TestPAE(t *testing.T) {
	t.Run("Empty", func(t *testing.T) {
		var want = []byte("DSSEv1 0  0 ")

		got := PAE("", "")
		assert.Equal(t, want, got, "Wrong encoding")
	})
	t.Run("Hello world", func(t *testing.T) {
		var want = []byte("DSSEv1 29 http://example.com/HelloWorld 11 hello world")

		got := PAE("http://example.com/HelloWorld", "hello world")
		assert.Equal(t, want, got, "Wrong encoding")
	})
}

type nilsigner int

func (n nilsigner) Sign(data []byte) ([]byte, string, error) {
	return data, "nil", nil
}

func (n nilsigner) Verify(keyID string, data, sig []byte) error {

	if keyID == "nil" {
		if len(data) != len(sig) {
			return errLength
		}

		for i := range data {
			if data[i] != sig[i] {
				return errVerify
			}
		}

		return nil
	}
	return ErrUnknownKey
}

func (m nilsigner) KeyID() (string, error) {
	return "", errors.New("KeyID not supported")
}

type nullsigner int

func (n nullsigner) Sign(data []byte) ([]byte, string, error) {
	return data, "null", nil
}

func (n nullsigner) Verify(keyID string, data, sig []byte) error {
	if keyID != "null" {
		return ErrUnknownKey
	}

	if len(data) != len(sig) {
		return errLength
	}

	for i := range data {
		if data[i] != sig[i] {
			return errVerify
		}
	}

	return nil
}

func (m nullsigner) KeyID() (string, error) {
	return "", errors.New("KeyID not supported")
}

type errsigner int

func (n errsigner) Sign(data []byte) ([]byte, string, error) {
	return nil, "", fmt.Errorf("signing error")
}

func (n errsigner) Verify(keyID string, data, sig []byte) error {
	return errVerify
}

func (n errsigner) KeyID() (string, error) {
	return "", errors.New("KeyID not supported")
}

type errverifier int

var errVerify = fmt.Errorf("test err verify")

func (n errverifier) Sign(data []byte) ([]byte, string, error) {
	return data, "err", nil
}

func (n errverifier) Verify(keyID string, data, sig []byte) error {
	return errVerify
}

func (n errverifier) KeyID() (string, error) {
	return "", errors.New("KeyID not supported")
}

type badverifier int

func (n badverifier) Sign(data []byte) ([]byte, string, error) {
	return append(data, byte(0)), "bad", nil
}

func (n badverifier) Verify(keyID string, data, sig []byte) error {
	if keyID != "bad" {
		return ErrUnknownKey
	}

	if len(data) != len(sig) {
		return errLength
	}

	for i := range data {
		if data[i] != sig[i] {
			return errVerify
		}
	}

	return nil
}

func (n badverifier) KeyID() (string, error) {
	return "", errors.New("KeyID not supported")
}

func TestNoSigners(t *testing.T) {
	t.Run("nil slice", func(t *testing.T) {
		signer, err := NewEnvelopeSigner(nil)
		assert.Nil(t, signer, "unexpected signer")
		assert.NotNil(t, err, "error expected")
		assert.Equal(t, ErrNoSigners, err, "wrong error")
	})

	t.Run("empty slice", func(t *testing.T) {
		signer, err := NewEnvelopeSigner([]SignVerifier{}...)
		assert.Nil(t, signer, "unexpected signer")
		assert.NotNil(t, err, "error expected")
		assert.Equal(t, ErrNoSigners, err, "wrong error")
	})
}

func TestNilSign(t *testing.T) {
	var keyID = "nil"
	var payloadType = "http://example.com/HelloWorld"
	var payload = "hello world"

	pae := PAE(payloadType, payload)
	want := Envelope{
		Payload:     base64.StdEncoding.EncodeToString([]byte(payload)),
		PayloadType: payloadType,
		Signatures: []Signature{
			{
				KeyID: keyID,
				Sig:   base64.StdEncoding.EncodeToString(pae),
			},
		},
	}

	var ns nilsigner
	signer, _ := NewEnvelopeSigner(ns)
	got, err := signer.SignPayload(payloadType, []byte(payload))
	assert.Nil(t, err, "sign failed")
	assert.Equal(t, &want, got, "bad signature")
}

func TestSignError(t *testing.T) {
	var es errsigner
	signer, _ := NewEnvelopeSigner(es)
	got, err := signer.SignPayload("t", []byte("d"))
	assert.Nil(t, got, "expected nil")
	assert.NotNil(t, err, "error expected")
	assert.Equal(t, "signing error", err.Error(), "wrong error")
}

func newEcdsaKey() *ecdsa.PrivateKey {
	var x big.Int
	var y big.Int
	var d big.Int

	_, ok := x.SetString("46950820868899156662930047687818585632848591499744589407958293238635476079160", 10)
	if !ok {
		return nil
	}
	_, ok = y.SetString("5640078356564379163099075877009565129882514886557779369047442380624545832820", 10)
	if !ok {
		return nil
	}
	_, ok = d.SetString("97358161215184420915383655311931858321456579547487070936769975997791359926199", 10)
	if !ok {
		return nil
	}

	var private = ecdsa.PrivateKey{
		PublicKey: ecdsa.PublicKey{
			Curve: elliptic.P256(),
			X:     &x,
			Y:     &y,
		},
		D: &d,
	}

	return &private
}

type EcdsaSigner struct {
	keyID    string
	key      *ecdsa.PrivateKey
	rLen     int
	verified bool
}

func (es *EcdsaSigner) Sign(data []byte) ([]byte, string, error) {
	// Data is complete message, hash it and sign the digest
	digest := sha256.Sum256(data)
	r, s, err := rfc6979.SignECDSA(es.key, digest[:], sha256.New)
	if err != nil {
		return nil, "", err
	}

	rb := r.Bytes()
	sb := s.Bytes()
	es.rLen = len(rb)
	rawSig := append(rb, sb...)

	return rawSig, es.keyID, nil
}

func (es *EcdsaSigner) Verify(keyID string, data, sig []byte) error {
	if keyID != es.keyID {
		return ErrUnknownKey
	}

	var r big.Int
	var s big.Int
	digest := sha256.Sum256(data)
	// Signature here is the raw bytes of r and s concatenated
	rb := sig[:es.rLen]
	sb := sig[es.rLen:]
	r.SetBytes(rb)
	s.SetBytes(sb)

	ok := ecdsa.Verify(&es.key.PublicKey, digest[:], &r, &s)
	es.verified = ok

	if ok {
		return nil
	}
	return errVerify
}

func (m *EcdsaSigner) KeyID() (string, error) {
	return m.keyID, nil
}

// Test against the example in the protocol specification:
// https://github.com/secure-systems-lab/dsse/blob/master/protocol.md
func TestEcdsaSign(t *testing.T) {
	var keyID = "test key 123"
	var payloadType = "http://example.com/HelloWorld"
	var payload = "hello world"
	var ecdsa = &EcdsaSigner{
		keyID: keyID,
		key:   newEcdsaKey(),
	}
	var want = Envelope{
		Payload:     "aGVsbG8gd29ybGQ=",
		PayloadType: payloadType,
		Signatures: []Signature{
			{
				KeyID: keyID,
				Sig:   "A3JqsQGtVsJ2O2xqrI5IcnXip5GToJ3F+FnZ+O88SjtR6rDAajabZKciJTfUiHqJPcIAriEGAHTVeCUjW2JIZA==",
			},
		},
	}

	signer, _ := NewEnvelopeSigner(ecdsa)
	env, err := signer.SignPayload(payloadType, []byte(payload))
	assert.Nil(t, err, "unexpected error")
	assert.Equal(t, &want, env, "Wrong envelope generated")

	// Now verify
	err = signer.Verify(env)
	assert.Nil(t, err, "unexpected error")
	assert.True(t, ecdsa.verified, "verify was not called")
}

func TestB64Decode(t *testing.T) {
	var want = make([]byte, 256)
	for i := range want {
		want[i] = byte(i)
	}
	var b64Url = "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8gISIjJCUmJygpKissLS4vMDEyMzQ1Njc4OTo7PD0+P0BBQkNERUZHSElKS0xNTk9QUVJTVFVWV1hZWltcXV5fYGFiY2RlZmdoaWprbG1ub3BxcnN0dXZ3eHl6e3x9fn+AgYKDhIWGh4iJiouMjY6PkJGSk5SVlpeYmZqbnJ2en6ChoqOkpaanqKmqq6ytrq+wsbKztLW2t7i5uru8vb6/wMHCw8TFxsfIycrLzM3Oz9DR0tPU1dbX2Nna29zd3t/g4eLj5OXm5+jp6uvs7e7v8PHy8/T19vf4+fr7/P3+/w=="
	var b64Std = "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8gISIjJCUmJygpKissLS4vMDEyMzQ1Njc4OTo7PD0-P0BBQkNERUZHSElKS0xNTk9QUVJTVFVWV1hZWltcXV5fYGFiY2RlZmdoaWprbG1ub3BxcnN0dXZ3eHl6e3x9fn-AgYKDhIWGh4iJiouMjY6PkJGSk5SVlpeYmZqbnJ2en6ChoqOkpaanqKmqq6ytrq-wsbKztLW2t7i5uru8vb6_wMHCw8TFxsfIycrLzM3Oz9DR0tPU1dbX2Nna29zd3t_g4eLj5OXm5-jp6uvs7e7v8PHy8_T19vf4-fr7_P3-_w=="
	var b64UrlErr = "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8gISIjJCUmJygpKissLS4vMDEyMzQ1Njc4OTo7PD0+P0BBQkNERUZHSElKS0xNTk9QUVJTVFVWV1hZWltcXV5fYGFiY2RlZmdoaWprbG1ub3BxcnN0dXZ3eHl6e3x9fn+AgYKDhIWGh4iJiouMjY6PkJGSk5SVlpeYmZqbnJ2en6ChoqOkpaanqKmqq6ytrq+wsbKztLW2t7i5uru8vb6/wMHCw8TFxsfIycrLzM3Oz9DR0tPU1dbX2Nna29zd3t/g4eLj5OXm5+jp6uvs7e7v8PHy8/T19vf4+fr7/P3+/w"
	var b64StdErr = "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8gISIjJCUmJygpKissLS4vMDEyMzQ1Njc4OTo7PD0-P0BBQkNERUZHSElKS0xNTk9QUVJTVFVWV1hZWltcXV5fYGFiY2RlZmdoaWprbG1ub3BxcnN0dXZ3eHl6e3x9fn-AgYKDhIWGh4iJiouMjY6PkJGSk5SVlpeYmZqbnJ2en6ChoqOkpaanqKmqq6ytrq-wsbKztLW2t7i5uru8vb6_wMHCw8TFxsfIycrLzM3Oz9DR0tPU1dbX2Nna29zd3t_g4eLj5OXm5-jp6uvs7e7v8PHy8_T19vf4-fr7_P3-_w"

	t.Run("Standard encoding", func(t *testing.T) {
		got, err := b64Decode(b64Std)
		assert.Nil(t, err, "unexpected error")
		assert.Equal(t, want, got, "wrong data")
	})
	t.Run("URL encoding", func(t *testing.T) {
		got, err := b64Decode(b64Url)
		assert.Nil(t, err, "unexpected error")
		assert.Equal(t, want, got, "wrong data")
	})

	t.Run("Standard encoding - error", func(t *testing.T) {
		got, err := b64Decode(b64StdErr)
		assert.NotNil(t, err, "expected error")
		assert.Nil(t, got, "wrong data")
	})
	t.Run("URL encoding - error", func(t *testing.T) {
		got, err := b64Decode(b64UrlErr)
		assert.NotNil(t, err, "expected error")
		assert.Nil(t, got, "wrong data")
	})
}

func TestVerifyOneProvider(t *testing.T) {
	var payloadType = "http://example.com/HelloWorld"
	var payload = "hello world"

	var ns nilsigner
	signer, _ := NewEnvelopeSigner(ns)
	env, err := signer.SignPayload(payloadType, []byte(payload))
	assert.Nil(t, err, "sign failed")

	err = signer.Verify(env)
	assert.Nil(t, err, "unexpected error")
}

func TestVerifyMultipleProvider(t *testing.T) {
	var payloadType = "http://example.com/HelloWorld"
	var payload = "hello world"

	var ns nilsigner
	var null nullsigner
	signer, _ := NewEnvelopeSigner(ns, null)
	env, err := signer.SignPayload(payloadType, []byte(payload))
	assert.Nil(t, err, "sign failed")

	err = signer.Verify(env)
	assert.Nil(t, err, "unexpected error")
}

func TestVerifyErr(t *testing.T) {
	var payloadType = "http://example.com/HelloWorld"
	var payload = "hello world"

	var errv errverifier
	signer, _ := NewEnvelopeSigner(errv)
	env, err := signer.SignPayload(payloadType, []byte(payload))
	assert.Nil(t, err, "sign failed")

	err = signer.Verify(env)
	assert.Equal(t, errVerify, err, "wrong error")
}

func TestBadVerifier(t *testing.T) {
	var payloadType = "http://example.com/HelloWorld"
	var payload = "hello world"

	var badv badverifier
	signer, _ := NewEnvelopeSigner(badv)
	env, err := signer.SignPayload(payloadType, []byte(payload))
	assert.Nil(t, err, "sign failed")

	err = signer.Verify(env)
	assert.NotNil(t, err, "expected error")
}

func TestVerifyNoSig(t *testing.T) {
	var badv badverifier
	signer, _ := NewEnvelopeSigner(badv)
	env := &Envelope{}

	err := signer.Verify(env)
	assert.Equal(t, ErrNoSignature, err, "wrong error")
}

func TestVerifyBadBase64(t *testing.T) {
	var badv badverifier
	signer, _ := NewEnvelopeSigner(badv)

	t.Run("Payload", func(t *testing.T) {
		env := &Envelope{
			Payload: "Not base 64",
			Signatures: []Signature{
				{},
			},
		}

		err := signer.Verify(env)
		assert.IsType(t, base64.CorruptInputError(0), err, "wrong error")
	})

	t.Run("Signature", func(t *testing.T) {
		env := &Envelope{
			Payload: "cGF5bG9hZAo=",
			Signatures: []Signature{
				{
					Sig: "not base 64",
				},
			},
		}

		err := signer.Verify(env)
		assert.IsType(t, base64.CorruptInputError(0), err, "wrong error")
	})
}

func TestVerifyNoMatch(t *testing.T) {
	var payloadType = "http://example.com/HelloWorld"

	var ns nilsigner
	var null nullsigner
	signer, _ := NewEnvelopeSigner(ns, null)
	env := &Envelope{
		PayloadType: payloadType,
		Payload:     "cGF5bG9hZAo=",
		Signatures: []Signature{
			{
				KeyID: "not found",
				Sig:   "cGF5bG9hZAo=",
			},
		},
	}

	err := signer.Verify(env)
	assert.NotNil(t, err, "expected error")
}

type interceptSigner struct {
	keyID        string
	verifyRes    bool
	verifyCalled bool
}

func (i *interceptSigner) Sign(data []byte) ([]byte, string, error) {
	return data, i.keyID, nil
}

func (i *interceptSigner) Verify(keyID string, data, sig []byte) error {
	i.verifyCalled = true
	if keyID != i.keyID {
		return ErrUnknownKey
	}

	if i.verifyRes {
		return nil
	}
	return errVerify
}

func (i *interceptSigner) KeyID() (string, error) {
	return i.keyID, nil
}

func TestVerifyOneFail(t *testing.T) {
	var payloadType = "http://example.com/HelloWorld"
	var payload = "hello world"

	var s1 = &interceptSigner{
		keyID:     "i1",
		verifyRes: true,
	}
	var s2 = &interceptSigner{
		keyID:     "i2",
		verifyRes: false,
	}
	signer, _ := NewEnvelopeSigner(s1, s2)
	env, err := signer.SignPayload(payloadType, []byte(payload))
	assert.Nil(t, err, "sign failed")

	err = signer.Verify(env)
	assert.NotNil(t, err, "expected error")
	assert.True(t, s1.verifyCalled, "verify not called")
	assert.True(t, s2.verifyCalled, "verify not called")
}
