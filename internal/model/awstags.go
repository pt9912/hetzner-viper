package model

import (
	"fmt"
	"regexp"
	"strings"
)

// The limits of AWS for tags (the Name and viper tags viper sets count).
const (
	maxAWSTags     = 50
	maxAWSTagKey   = 128
	maxAWSTagValue = 256
)

// AWSTag is one KEY=VALUE pair of AWS_TAGS or AWS_TAGS_SHARED.
type AWSTag struct{ Key, Value string }

// awsTagChars is what AWS allows in a tag key and value: letters, digits, spaces and _ . : / = + - @.
var awsTagChars = regexp.MustCompile(`^[\p{L}\p{Z}\p{N}_.:/=+\-@]*$`)

// ParseAWSTags reads the setting of that name (AWS_TAGS or AWS_TAGS_SHARED), comma-separated KEY=VALUE pairs (a value cannot contain a comma),
// and returns them in order. The keys Name and viper are viper's own and cannot be set.
func ParseAWSTags(setting, s string) ([]AWSTag, error) {
	var out []AWSTag
	seen := map[string]bool{}
	for _, pair := range strings.Split(s, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		k, v, ok := strings.Cut(pair, "=")
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		switch {
		case !ok || k == "":
			return nil, fmt.Errorf("%s: %q is not KEY=VALUE (a value cannot contain a comma)", setting, pair)
		case k == "Name" || k == "viper":
			return nil, fmt.Errorf("%s: the key %s is reserved for viper", setting, k)
		case strings.HasPrefix(strings.ToLower(k), "aws:"):
			return nil, fmt.Errorf("%s: the key %s starts with aws:, which AWS keeps for itself", setting, k)
		case len([]rune(k)) > maxAWSTagKey:
			return nil, fmt.Errorf("%s: the key %.20s... is longer than %d characters", setting, k, maxAWSTagKey)
		case len([]rune(v)) > maxAWSTagValue:
			return nil, fmt.Errorf("%s: the value of %s is longer than %d characters", setting, k, maxAWSTagValue)
		case !awsTagChars.MatchString(k) || !awsTagChars.MatchString(v):
			return nil, fmt.Errorf("%s: %s=%s has a character AWS does not allow (letters, digits, spaces and _ . : / = + - @ only)", setting, k, v)
		case seen[k]:
			return nil, fmt.Errorf("%s: the key %s is set twice", setting, k)
		}
		seen[k] = true
		out = append(out, AWSTag{k, v})
	}
	if n := len(out) + 2; n > maxAWSTags {
		return nil, fmt.Errorf("%s: %d tags and viper's own two are more than the %d AWS allows", setting, len(out), maxAWSTags)
	}
	return out, nil
}
