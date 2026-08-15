package device

import "regexp"

func regexpPrefix(prefix string) *regexp.Regexp {
	return regexp.MustCompile("^" + regexp.QuoteMeta(prefix))
}
