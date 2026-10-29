package aws

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/pt9912/hetzner-viper/internal/model"
	"github.com/pt9912/hetzner-viper/internal/port/driven/cloud"
)

// fingerprint is how EC2 names an imported ED25519 key: the SHA-256 of the key, base64 without
// padding. It is "" for a line that is no OpenSSH key.
func fingerprint(pub string) string {
	f := strings.Fields(pub)
	if len(f) < 2 {
		return ""
	}
	blob, err := base64.StdEncoding.DecodeString(f[1])
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(blob)
	return base64.RawStdEncoding.EncodeToString(sum[:])
}

// sameFingerprint compares a fingerprint EC2 gives for an imported key with the key: the SHA-256
// of an ED25519 key ("SHA256:…", padded or not), the MD5 of other keys (RFC 4716, colon-separated
// hex, as ssh-keygen -E md5 shows it).
func sameFingerprint(ec2fp, pub string) bool {
	if ours := fingerprint(pub); ours != "" && strings.TrimRight(strings.TrimPrefix(ec2fp, "SHA256:"), "=") == ours {
		return true
	}
	md5fp := model.FingerprintMD5(pub)
	return md5fp != "" && strings.EqualFold(strings.TrimPrefix(ec2fp, "MD5:"), md5fp)
}

// keys lists the key pairs of the region.
func (c *Client) keys(ctx context.Context) ([]types.KeyPairInfo, error) {
	a, err := c.api(ctx)
	if err != nil {
		return nil, err
	}
	out, err := a.ec2.DescribeKeyPairs(ctx, &ec2.DescribeKeyPairsInput{})
	if err != nil {
		return nil, err
	}
	return out.KeyPairs, nil
}

// EnsureLoginKey makes sure the public key is a key pair of the region and returns the name it
// has there: a key that is imported already is reused under whatever name it has.
func (c *Client) EnsureLoginKey(ctx context.Context, name, publicKey string) (model.LoginKey, error) {
	ks, err := c.keys(ctx)
	if err != nil {
		return model.LoginKey{}, &cloud.OpError{Op: "listing the key pairs", Err: err}
	}
	for _, k := range ks {
		if sameFingerprint(str(k.KeyFingerprint), publicKey) {
			return model.LoginKey{Ref: str(k.KeyName)}, nil
		}
	}
	for _, k := range ks {
		if str(k.KeyName) == name {
			return model.LoginKey{}, &cloud.KeyConflictError{Name: name}
		}
	}
	a, err := c.api(ctx)
	if err != nil {
		return model.LoginKey{}, &cloud.OpError{Op: "reaching AWS", Err: err}
	}
	shared, err := awsTags("AWS_TAGS_SHARED", c.opts.SharedTags)
	if err != nil {
		return model.LoginKey{}, err
	}
	_, err = a.ec2.ImportKeyPair(ctx, &ec2.ImportKeyPairInput{
		KeyName: ptr(name), PublicKeyMaterial: []byte(strings.TrimSpace(publicKey)),
		TagSpecifications: []types.TagSpecification{{ResourceType: types.ResourceTypeKeyPair, Tags: append([]types.Tag{{Key: ptr("viper"), Value: ptr(name)}}, shared...)}},
	})
	if err != nil {
		return model.LoginKey{}, &cloud.OpError{Op: fmt.Sprintf("uploading the key pair %s", name), Write: true, Err: err}
	}
	return model.LoginKey{Ref: name, Created: true}, nil
}
