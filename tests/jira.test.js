const fs = require('fs');
/* Unit tests for the Jira wiki markup renderer in web/assets/jira.js.
 *
 *   node tests/jira.test.js [some-file.md]
 *
 * With no argument it runs the assertions only. Given a Markdown file it also
 * renders every fenced block in it and fails on a leaked placeholder or any
 * script/handler/javascript: URL reaching the output. No dependencies: the
 * renderer is a browser IIFE, so a bare `window` object is all it needs.
 */
const path = require('path');
global.window = {};
const RENDERER = path.join(__dirname, '..', 'web', 'assets', 'jira.js');
new Function(fs.readFileSync(RENDERER, 'utf8'))();
const J = global.window.__mdlJira;

let pass = 0, fail = 0;
function is(name, got, want) {
  if (got === want) { pass++; return; }
  fail++;
  console.log('FAIL ' + name + '\n  got:  ' + got + '\n  want: ' + want);
}
function has(name, got, needle) {
  if (got.includes(needle)) { pass++; return; }
  fail++;
  console.log('FAIL ' + name + '\n  got:  ' + got + '\n  need: ' + needle);
}
function hasnt(name, got, needle) {
  if (!got.includes(needle)) { pass++; return; }
  fail++;
  console.log('FAIL ' + name + '\n  got:  ' + got + '\n  must not contain: ' + needle);
}

// ---- detection
is('detect h3', J.looksLikeJira('h3. Hello'), true);
is('detect table', J.looksLikeJira('||a||b||'), true);
is('detect mono', J.looksLikeJira('use {{code}} here'), true);
is('detect mention', J.looksLikeJira('[~accountid:712020:abc]'), true);
is('detect bq', J.looksLikeJira('bq. quoted'), true);
is('plain prose is not jira', J.looksLikeJira('Just a sentence with *bold* and _italic_ words.'), false);
is('shell text is not jira', J.looksLikeJira('git log --oneline | head -5'), false);
is('markdown is not jira', J.looksLikeJira('# Heading\n\n- item\n- item'), false);

// ---- headings
is('h3', J.render('h3. Blue Yonder'), '<h3>Blue Yonder</h3>');
is('h1..h6', J.render('h1. a\nh6. b'), '<h1>a</h1><h6>b</h6>');

// ---- emphasis
is('bold', J.render('a *bold* word'), '<p>a <strong>bold</strong> word</p>');
is('italic', J.render('a _it_ word'), '<p>a <em>it</em> word</p>');
is('strike', J.render('a -no- word'), '<p>a <del>no</del> word</p>');
is('underline', J.render('a +ins+ word'), '<p>a <ins>ins</ins> word</p>');
is('mono', J.render('a {{tt}} word'), '<p>a <code class="jira-mono">tt</code> word</p>');
is('bold at line start', J.render('*bold* start'), '<p><strong>bold</strong> start</p>');
hasnt('snake_case untouched', J.render('the some_var_name here'), '<em>');
hasnt('iso date untouched', J.render('on 2026-09-10 it ran'), '<del>');
hasnt('ticket key untouched', J.render('see TTN-144799 for detail'), '<del>');
hasnt('email untouched', J.render('mail bofan.li@sap.com now'), '<em>');
is('escaped star', J.render('a \\*not bold\\* word'), '<p>a *not bold* word</p>');

// ---- mentions and links
has('accountid mention', J.render('[~accountid:712020:fe16] hi'), '<span class="jira-mention" title="accountid 712020:fe16">@mention</span>');
has('named mention', J.render('[~baris] hi'), '@baris');
has('labelled link', J.render('[the ticket|https://x.test/a]'), '<a href="https://x.test/a" rel="noreferrer noopener" target="_blank">the ticket</a>');
has('bare link', J.render('[https://x.test/a]'), 'href="https://x.test/a"');
is('non-url pipe left alone', J.render('[foo|bar]'), '<p>[foo|bar]</p>');
hasnt('javascript scheme refused', J.render('[click|javascript:alert(1)]'), '<a ');
has('ampersand in url not double-escaped', J.render('[q|https://x.test/?a=1&b=2]'), 'href="https://x.test/?a=1&amp;b=2"');

// ---- injection
hasnt('html escaped', J.render('<script>alert(1)</script>'), '<script>');
has('html shown as text', J.render('<b>x</b>'), '&lt;b&gt;x&lt;/b&gt;');
hasnt('onerror attribute escaped', J.render('<img src=x onerror=alert(1)>'), '<img');
hasnt('colour cannot break out', J.render('{color:red" onload="x}z{color}'), 'onload=');

// ---- lists
is('flat bullets', J.render('* a\n* b'), '<ul><li>a</li><li>b</li></ul>');
is('nested bullets', J.render('* a\n** b\n* c'), '<ul><li>a<ul><li>b</li></ul></li><li>c</li></ul>');
is('numbered', J.render('# one\n# two'), '<ol><li>one</li><li>two</li></ol>');
is('mixed kinds', J.render('* a\n# b'), '<ul><li>a</li></ul><ol><li>b</li></ol>');
is('bullet with bold', J.render('* *x* y'), '<ul><li><strong>x</strong> y</li></ul>');

// ---- tables
is('header + row', J.render('||A||B||\n|1|2|'),
   '<table class="jira-table"><tr><th>A</th><th>B</th></tr><tr><td>1</td><td>2</td></tr></table>');
has('table cell formatting', J.render('||A||\n|*x*|'), '<td><strong>x</strong></td>');

// ---- blocks
is('bq', J.render('bq. quoted'), '<blockquote><p>quoted</p></blockquote>');
has('quote block', J.render('{quote}\nhello\n{quote}'), '<blockquote><p>hello</p></blockquote>');
has('code block', J.render('{code:sql}\nSELECT 1;\n{code}'), '<pre>SELECT 1;</pre>');
has('code lang label', J.render('{code:sql}\nSELECT 1;\n{code}'), '<div class="jira-code-lang">sql</div>');
hasnt('code contents not formatted', J.render('{code}\na *b* c\n{code}'), '<strong>');
has('noformat', J.render('{noformat}\nraw *text*\n{noformat}'), 'raw *text*');
has('panel title', J.render('{panel:title=Notes}\nbody\n{panel}'), '<div class="jira-panel-title">Notes</div>');
is('hr', J.render('----'), '<hr>');
is('forced break', J.render('a\\\\b'), '<p>a<br>b</p>');

// ---- paragraphs: a single newline is a line break in Jira
is('single newline is a break', J.render('one\ntwo'), '<p>one<br>two</p>');
is('blank line splits paragraphs', J.render('one\n\ntwo'), '<p>one</p><p>two</p>');

// ---- optional: every fenced block of a real document
const sample = process.argv[2];
if (sample) {
  const real = fs.readFileSync(sample, 'utf8');
  const fence = new RegExp('^```([A-Za-z0-9_+-]*)[ \\t]*\\r?\\n([\\s\\S]*?)^```[ \\t]*$', 'gm');
  const LEAK = new RegExp('\\u0001');
  let m, n = 0;
  while ((m = fence.exec(real)) !== null) {
    n++;
    const body = m[2];
    const html = J.render(body);
    console.log('  block ' + n + ': jira=' + J.looksLikeJira(body) +
      ' ' + body.length + ' chars -> ' + html.length + ' html');
    if (LEAK.test(html)) { fail++; console.log('FAIL: placeholder leaked into block ' + n); }
    if (/<script|\son\w+=|javascript:/i.test(html)) { fail++; console.log('FAIL: unsafe output in block ' + n); }
  }
}

console.log('\n' + pass + ' passed, ' + fail + ' failed');
process.exit(fail ? 1 : 0);
