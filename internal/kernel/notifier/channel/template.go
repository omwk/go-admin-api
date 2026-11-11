package channel

import (
	"bytes"
	"errors"
	html "html/template"
	"regexp"
	"strings"
	"text/template"
	"text/template/parse"
)

var variableName = regexp.MustCompile(`^[A-Z][A-Za-z0-9_]{0,63}$`)

// Render accepts literal text and direct whitelisted variable substitutions only.
func Render(t Template, variables map[string]string) (Message, error) {
	if !t.Enabled || (t.Format != "text" && t.Format != "html") {
		return Message{}, errors.New("template unavailable")
	}
	subject, err := renderPart(t.Subject, variables, false)
	if err != nil || strings.ContainsAny(subject, "\r\n") {
		return Message{}, errors.New("template rendering failed")
	}
	body, err := renderPart(t.Body, variables, t.Format == "html")
	if err != nil || len(body) > 128*1024 {
		return Message{}, errors.New("template rendering failed")
	}
	return Message{Subject: subject, Body: body, Format: t.Format, Variables: variables}, nil
}

func renderPart(source string, variables map[string]string, escape bool) (string, error) {
	t, err := template.New("notification").Option("missingkey=error").Parse(source)
	if err != nil {
		return "", errors.New("template syntax invalid")
	}
	if err := validateNodes(t.Tree.Root, variables); err != nil {
		return "", err
	}
	var b bytes.Buffer
	if escape {
		h, err := html.New("notification").Option("missingkey=error").Parse(source)
		if err != nil {
			return "", errors.New("template syntax invalid")
		}
		err = h.Execute(&b, variables)
		return b.String(), err
	}
	err = t.Execute(&b, variables)
	return b.String(), err
}

func validateNodes(root *parse.ListNode, variables map[string]string) error {
	for _, node := range root.Nodes {
		switch n := node.(type) {
		case *parse.TextNode:
		case *parse.ActionNode:
			if n.Pipe.Decl != nil || len(n.Pipe.Cmds) != 1 || len(n.Pipe.Cmds[0].Args) != 1 {
				return errors.New("template expression forbidden")
			}
			field, ok := n.Pipe.Cmds[0].Args[0].(*parse.FieldNode)
			if !ok || len(field.Ident) != 1 || !variableName.MatchString(field.Ident[0]) {
				return errors.New("template variable forbidden")
			}
			if _, ok := variables[field.Ident[0]]; !ok {
				return errors.New("template variable unknown")
			}
		default:
			return errors.New("template expression forbidden")
		}
	}
	return nil
}
