package benchmark

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRepairPreviewHTMLObjectProperties(t *testing.T) {
	for _, script := range []string{
		`mk('circle',{r:110,fill:'none',stroke='#6e655b','stroke-width':3},g);`,
		`const options={first=1,second=2,nested:{color='red'}};`,
		"// 中文\r\nconst options={颜色='红色'};",
		"// comment\rconst options={x=1};",
		"// comment\u2028const options={x=1};",
		"// comment\u2029const options={x=1};",
		`const options={x /* = in comment */ = 1};`,
	} {
		t.Run(script, func(t *testing.T) {
			document := "<!doctype html>\n<html><body><script>" + script + "</script></body></html>"
			fixed := RepairPreviewHTML(document)
			require.NotEmpty(t, fixed)
			require.Equal(t, len(document), len(fixed))
			changes := 0
			for at := range document {
				if document[at] != fixed[at] {
					require.Equal(t, byte('='), document[at])
					require.Equal(t, byte(':'), fixed[at])
					changes++
				}
			}
			require.Positive(t, changes)
			require.Empty(t, RepairPreviewHTML(fixed), "already valid HTML must not be marked repaired")
		})
	}
}

func TestRepairPreviewHTMLPreservesValidSource(t *testing.T) {
	for _, script := range []string{
		`const {x=1,y:{z=2}={}}=source;`,
		`({x=1}=source);`,
		`const fn=({x=1}={})=>x;`,
		`function fn(x=1){return x} class Box { x=1; }`,
		`let x; x=2; const o={x:x=3};`,
		`const s="{stroke='red'}"; /* {x=1} */ // {y=2}`,
		"const s=`{stroke='red'} ${(() => {let x; return x=1})()}`;",
		`const r=/{stroke='red'}/;`,
		`const o={x:1, method(a=1){return a}};`,
	} {
		t.Run(script, func(t *testing.T) {
			document := "<html><body><script>" + script + "</script></body></html>"
			require.Empty(t, RepairPreviewHTML(document))
			// A separate invalid script cannot cause valid defaults, strings,
			// regular expressions, comments or attributes to be rewritten.
			mixed := strings.Replace(document, "</body>", `<svg stroke="red"></svg><script>const broken={y=2};</script></body>`, 1)
			want := strings.Replace(mixed, "const broken={y=2}", "const broken={y:2}", 1)
			require.Equal(t, want, RepairPreviewHTML(mixed))
		})
	}
}

func TestRepairPreviewHTMLScriptKindsAndExactBytes(t *testing.T) {
	document := "<!DOCTYPE html>\r\n<html><head><!-- <script>{x=1}</script> -->" +
		`<script type="application/json">{"stroke":"x=y"}</script>` +
		`<script src="/external.js">const ignored={x=1};</script>` +
		`<template><script>const inert={x=1};</script></template>` +
		`<script type="text/plain">const data={x=1};</script>` +
		`<script language="json">const data={x=1};</script>` +
		`<SCRIPT TYPE="module">const first={x=1};</SCRIPT>` +
		`<script type="application/javascript">const second={y=2};</script>` +
		"</head><body data-example=\"x=y\">中文 &amp; text</body></html>"
	want := strings.Replace(document, "const first={x=1}", "const first={x:1}", 1)
	want = strings.Replace(want, "const second={y=2}", "const second={y:2}", 1)
	require.Equal(t, want, RepairPreviewHTML(document))
}

func TestRepairPreviewHTMLRejectsUncertainOrUnboundedChanges(t *testing.T) {
	for name, document := range map[string]string{
		"switch colon":            `<html><script>switch (1) { default = console.log('not an object'); }</script></html>`,
		"destructuring colon":     `<html><script>function f({'x'=y}){return y;}</script></html>`,
		"quoted key ambiguity":    `<html><script>const o={'stroke'='red'};</script></html>`,
		"incomplete trailing tag": `<html><script>const o={x=1};</script></html><div data-incomplete="`,
		"self closing script":     `<html><script/>const missed={x=1};</script><script>const o={y=2};</script></html>`,
		"self closing template":   `<html><template/><script>const inert={x=1};</script></template><script>const o={y=2};</script></html>`,
		"other syntax":            `<html><script>const o={x:1 y:2};</script></html>`,
		"unclosed script syntax":  `<html><script>const o={x=1};function (</script></html>`,
		"other script invalid":    `<html><script>const o={x=1};</script><script>const = ;</script></html>`,
		"too many repairs":        "<html><script>const o={" + strings.Repeat("x=1,", 17) + "};</script></html>",
		"too many scripts":        "<html>" + strings.Repeat("<script>let x=1;</script>", 65) + "<script>({x=1});</script></html>",
		"too large":               strings.Repeat(" ", MaxHTMLBytes) + `<html><script>({x=1});</script></html>`,
		"parse budget":            `<html><script>const o={` + strings.Repeat("x=1,", 8) + "};" + strings.Repeat(" ", MaxHTMLBytes-200) + `</script></html>`,
		"invalid UTF8":            "<html>\xff<script>({x=1});</script></html>",
		"null":                    "<html>\x00<script>({x=1});</script></html>",
	} {
		t.Run(name, func(t *testing.T) { require.Empty(t, RepairPreviewHTML(document)) })
	}
}

func BenchmarkRepairPreviewHTML(b *testing.B) {
	document := "<html><script>" + strings.Repeat("/* drawing code */\n", 900) + `const o={stroke='red'};</script></html>`
	for b.Loop() {
		if RepairPreviewHTML(document) == "" {
			b.Fatal("preview was not repaired")
		}
	}
}
