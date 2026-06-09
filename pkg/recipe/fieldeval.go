package recipe

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/PaesslerAG/jsonpath"
	"github.com/PuerkitoBio/goquery"
)

// Evaluate resolves a single string value from a FieldExtractor against a
// PageContext. It walks fx.From in order, returning the first source that
// yields a non-empty value after fx.Transform is applied. Returns "" (no error)
// when nothing resolves — required-ness is enforced later by opportunity.Verify.
func Evaluate(fx FieldExtractor, pc *PageContext) (string, error) {
	for _, src := range fx.From {
		raw, err := resolveRaw(src, fx, pc)
		if err != nil {
			return "", err
		}
		if raw == "" {
			continue
		}
		out, err := applyTransforms(raw, fx.Transform, transformCtx{BaseURL: pc.URL})
		if err != nil {
			return "", err
		}
		if out != "" {
			return out, nil
		}
	}
	return "", nil
}

// EvaluateList resolves a multi-valued field (e.g. categories/skills). For
// selector sources it returns the text of every match; for json_ld/next_data/
// record it expects the JSONPath to resolve to an array.
func EvaluateList(fx FieldExtractor, pc *PageContext) ([]string, error) {
	for _, src := range fx.From {
		switch src {
		case "selector":
			if pc.HTML == nil || fx.Selector == "" {
				continue
			}
			var out []string
			pc.HTML.Find(fx.Selector).Each(func(_ int, s *goquery.Selection) {
				if v := strings.TrimSpace(s.Text()); v != "" {
					out = append(out, v)
				}
			})
			if len(out) > 0 {
				return out, nil
			}
		case "json_ld":
			for _, blob := range pc.JSONLD {
				if list := jsonPathList(fx.JSONPath, blob); len(list) > 0 {
					return list, nil
				}
			}
		case "next_data":
			if list := jsonPathList(fx.JSONPath, pc.NextData); len(list) > 0 {
				return list, nil
			}
		case "record":
			if list := jsonPathList(fx.JSONPath, pc.Record); len(list) > 0 {
				return list, nil
			}
		}
	}
	return nil, nil
}

// resolveRaw returns the untransformed value for one From source.
func resolveRaw(src string, fx FieldExtractor, pc *PageContext) (string, error) {
	switch src {
	case "const":
		return fx.Const, nil
	case "meta":
		return pc.Meta[fx.Meta], nil
	case "selector":
		if pc.HTML == nil || fx.Selector == "" {
			return "", nil
		}
		sel := pc.HTML.Find(fx.Selector).First()
		if sel.Length() == 0 {
			return "", nil
		}
		if fx.Attr != "" {
			v, _ := sel.Attr(fx.Attr)
			return strings.TrimSpace(v), nil
		}
		return strings.TrimSpace(sel.Text()), nil
	case "microdata":
		if pc.HTML == nil || fx.Microdata == "" {
			return "", nil
		}
		sel := pc.HTML.Find(fmt.Sprintf("[itemprop=%q]", fx.Microdata)).First()
		if sel.Length() == 0 {
			return "", nil
		}
		if v, ok := sel.Attr("content"); ok {
			return strings.TrimSpace(v), nil
		}
		return strings.TrimSpace(sel.Text()), nil
	case "json_ld":
		for _, blob := range pc.JSONLD {
			if v := jsonPathScalar(fx.JSONPath, blob); v != "" {
				return v, nil
			}
		}
		return "", nil
	case "next_data":
		return jsonPathScalar(fx.JSONPath, pc.NextData), nil
	case "record":
		return jsonPathScalar(fx.JSONPath, pc.Record), nil
	default:
		return "", fmt.Errorf("unknown From source %q", src)
	}
}

// jsonPathScalar evaluates path against root and renders a scalar result. A
// missing path or non-scalar result yields "".
func jsonPathScalar(path string, root any) string {
	if path == "" || root == nil {
		return ""
	}
	v, err := jsonpath.Get(path, root)
	if err != nil || v == nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return strings.TrimSpace(t)
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(t)
	default:
		return ""
	}
}

func jsonPathList(path string, root any) []string {
	if path == "" || root == nil {
		return nil
	}
	v, err := jsonpath.Get(path, root)
	if err != nil {
		return nil
	}
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	var out []string
	for _, item := range arr {
		if s, ok := item.(string); ok {
			if s = strings.TrimSpace(s); s != "" {
				out = append(out, s)
			}
		}
	}
	return out
}
