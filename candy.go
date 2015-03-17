package main

import (
	"bufio"
	"bytes"
	"flag"
	"fmt"
	"github.com/russross/blackfriday"
	"html/template"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	PageDir     = "pages"
	TempalteDir = "templates"
	SectionDir  = "sections"
)

var (
	flagIsStartServer = flag.Bool("serve", false, "Start local testing server.")
	flagServePort     = flag.Int("port", 8080, "The port to serve.")
	flagOutputPath    = flag.String("output_path", "www", "Path to output")
)

type Context struct {
	TemplateStack []string
	Dict          map[string]interface{}
	Sections      map[string][]byte
}

func newContext() *Context {
	return &Context{
		TemplateStack: make([]string, 0),
		Dict:          make(map[string]interface{}),
		Sections:      make(map[string][]byte),
	}
}

type Site struct {
	outputPath string
}

func isMarkdown(path string) bool {
	return strings.HasSuffix(path, ".md")
}

func isHTML(path string) bool {
	return strings.HasSuffix(path, ".html")
}

func (site Site) readRawFile(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return ioutil.ReadAll(f)
}

var spaceRe = regexp.MustCompile("\\s+")

func (site Site) parseMeta(ctx *Context, rawContent []byte) ([]byte, error) {
	const metaStart = "<!--!"
	const metaEnd = "-->"
	text := string(rawContent)
	if !strings.HasPrefix(text, metaStart) {
		return rawContent, nil
	}
	parts := strings.SplitN(text[len(metaStart):], metaEnd, 2)
	if len(parts) != 2 {
		return rawContent, nil
	}
	mainContent := []byte(strings.TrimLeft(parts[1], " \r\n\t"))

	metaReader := bufio.NewReader(bytes.NewBuffer([]byte(
		strings.Trim(parts[0], " \r\n\t") + "\n")))
	for line, err := metaReader.ReadBytes('\n'); err == nil; line, err = metaReader.ReadBytes('\n') {
		cmd := spaceRe.Split(strings.Trim(string(line), " \r\n\t"), 2)
		if len(cmd) != 2 {
			continue
		}
		switch cmd[0] {
		case "@template":
			ctx.TemplateStack = append(ctx.TemplateStack, spaceRe.Split(cmd[1], -1)...)
		case "@section", "@append":
			pair := spaceRe.Split(cmd[1], 2)
			if len(pair) == 2 {
				_, has := ctx.Sections[pair[0]]
        if !has || cmd[0] == "@append" {
					savedTemplateStaack := ctx.TemplateStack
					ctx.TemplateStack = nil
					section, err := site.loadDocument(ctx, filepath.Join(SectionDir, pair[1]))
					if err != nil {
						return nil, err
					}
					ctx.TemplateStack = savedTemplateStaack
          sectionName := pair[0]
          ctx.Sections[sectionName] = append(ctx.Sections[sectionName], section...)
				}
			}
		default:
			if _, has := ctx.Dict[cmd[0]]; !has {
				ctx.Dict[cmd[0]] = cmd[1]
			}
		}
	}
	return mainContent, nil
}

func (site Site) loadDocument(ctx *Context, path string) ([]byte, error) {
	var innerDocument []byte
	for {
		rawContent, err := site.readRawFile(path)
		if err != nil {
			return nil, err
		}
		if !(isMarkdown(path) || isHTML(path)) {
			return rawContent, nil
		}

		content, err := site.parseMeta(ctx, rawContent)
		if err != nil {
			return nil, err
		}
		if isMarkdown(path) {
			content = blackfriday.MarkdownCommon(content)
		}
		if innerDocument != nil {
			content = []byte(strings.Replace(
				string(content), "{{#INNER_DOCUMENT}}", string(innerDocument), -1))
		}
		if len(ctx.TemplateStack) > 0 {
			// recur
			templateName := ctx.TemplateStack[len(ctx.TemplateStack)-1]
			ctx.TemplateStack = ctx.TemplateStack[:len(ctx.TemplateStack)-1]
			path = filepath.Join(TempalteDir, templateName)
			innerDocument = content
			continue
		} else {
			return content, nil
		}
	}
}

var sectionRe = regexp.MustCompile("\\{\\{#[^\\}]+\\}\\}")

func (site Site) renderDocument(path string) ([]byte, error) {
	ctx := newContext()
	content, err := site.loadDocument(ctx, path)
	if err != nil {
		return nil, err
	}
	tplContent := string(content)
	for replaced := true; replaced; {
		replaced = false
		tplContent = sectionRe.ReplaceAllStringFunc(tplContent, func(ref string) string {
			sectionName := ref[3 : len(ref)-2]
			if section, ok := ctx.Sections[sectionName]; ok {
				replaced = true
				return string(section)
			} else {
				return ""
			}
		})
	}
	tpl, err := template.New(path).Parse(string(tplContent))
	if err != nil {
		return nil, err
	}
	buf := new(bytes.Buffer)
	if err := tpl.Execute(buf, ctx.Dict); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (site Site) Generate() {
	filepath.Walk(PageDir, func(path string, info os.FileInfo, err error) error {
		pathSuffix := PageDir + string(filepath.Separator)
		if info == nil || info.IsDir() || !strings.HasPrefix(path, pathSuffix) {
			return nil
		}
		var content []byte
		if isMarkdown(path) || isHTML(path) {
			content, err = site.renderDocument(path)
		} else {
			content, err = site.readRawFile(path)
		}
		if err != nil {
			log.Printf("Handling %s failed, %v, skip.", path, err)
			return nil
		}
		relPath := path[len(pathSuffix):]
		destPath := filepath.Join(site.outputPath, relPath)
		if isMarkdown(destPath) {
			destPath = destPath[0:len(destPath)-len(".md")] + ".html"
		}
		if err = os.MkdirAll(filepath.Dir(destPath), os.ModeDir|0755); err != nil {
			log.Printf("Cannot create dir for dest %s, %v, skip", destPath, err)
			return nil
		}
		if err = ioutil.WriteFile(destPath, content, 0644); err != nil {
			log.Printf("Cannot write file %s to %s, %v", path, destPath, err)
			return nil
		}
		fmt.Printf("%s -> %s\n", relPath, destPath)
		return nil
	})
}

func main() {
	flag.Parse()
	site := Site{
		outputPath: *flagOutputPath,
	}
	site.Generate()

	if *flagIsStartServer {
		fs := http.FileServer(http.Dir(*flagOutputPath))
		http.Handle("/", fs)
		log.Printf("Start debug server at %d", *flagServePort)
		http.ListenAndServe(fmt.Sprintf(":%d", *flagServePort), nil)
	}
}
