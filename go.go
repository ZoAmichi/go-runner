// Copyright 2009 Dimiter Stanev, malkia@gmail.com. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"os"
	"fmt"
	"container/list"
	"go/parser"
	"go/ast"
	"go/token"
	"bufio"
	"runtime"
	"strconv"
	"strings"
	"path"
)

var (
	curdir, _ = os.Getwd()
	gobin     = os.Getenv("GOBIN")
	gopkg     = ""
	arch      = ""
)

func init() {
	goos := os.Getenv("GOOS")
	if goos == "" {
		goos = runtime.GOOS
	}
	goarch := os.Getenv("GOARCH")
	if goarch == "" {
		goarch = runtime.GOARCH
	}
	gopkg = path.Join(runtime.GOROOT(), "pkg", goos+"_"+goarch)
	// TODO no exist to panic
	if v, ok := map[string]string{"amd64": "6", "386": "8", "arm": "5"}[goarch]; ok {
		arch = v
	} else {
		arch = ""
	}
}


// source
type source struct {
	filepath    string
	packageName string
	imports     []string
	mtime_ns    int64
}

func newSource(filepath string) (*source, os.Error) {
	s := new(source)
	s.filepath = filepath
	s.imports = make([]string, 0)

	// mtime_ns
	if stat, error := os.Lstat(filepath); error != nil {
		return nil, error
	} else {
		s.mtime_ns = stat.Mtime_ns
	}

	file, error := parser.ParseFile(token.NewFileSet(), filepath, nil, parser.ImportsOnly)
	if error != nil {
		return nil, error
	}

	// packageName
	s.packageName = file.Name.Name

	// imports
	for _, decl := range file.Decls {
		decl, ok := decl.(*ast.GenDecl)
		if !ok {
			continue
		}
		for _, spec := range decl.Specs {
			spec, ok := spec.(*ast.ImportSpec)
			if !ok {
				continue
			}
			importName, _ := strconv.Unquote(string(spec.Path.Value))
			importName = path.Clean(importName)
			s.imports = append(s.imports, importName)
		}
	}

	return s, nil
}

// target
type target struct {
	ctx            *context
	targetName     string
	importName     string
	objectDir      string
	files          map[string]*source
	imports        *list.List // List<*Target>
	packagePaths   map[string]string
	shouldUpdate   bool
	ensureSources  bool
	isLocalPackage bool
}

func newTarget(ctx *context, targetName string, importName string) *target {
	t := new(target)
	t.ctx = ctx
	t.targetName = targetName
	t.importName = importName
	t.objectDir = ""
	t.files = make(map[string]*source)
	t.imports = list.New()
	t.packagePaths = make(map[string]string)
	t.shouldUpdate = false
	t.ensureSources = false
	t.isLocalPackage = true
	return t
}

func (self *target) reflesh() os.Error {
	// installed package
	if self.importName != "main" {
		obj := path.Join(gopkg, self.importName) + ".a"
		if self.ctx.fileExists(obj) {
			self.objectDir, _ = path.Split(obj)
			self.objectDir = path.Clean(self.objectDir)
			self.shouldUpdate = false
			self.isLocalPackage = false
			return nil
		}
		// TODO check other package dirs
	}
	// find local package sources
	dir, packageName := path.Split(self.importName)
	dir = path.Clean(dir)
	if !self.ensureSources {
		for _, v := range self.ctx.listDir(dir) {
			s := path.Join(dir, v)
			if path.Ext(s) != ".go" || strings.HasSuffix(s, "_test.go") {
				continue
			}
			if _, exist := self.files[s]; !exist {
				src, err := self.ctx.getSource(s)
				if err != nil {
					return err
				}
				if src != nil {
					if src.packageName == packageName {
						self.files[src.filepath] = src
					}
				}
			}
		}
	}

	if len(self.files) < 1 {
		return os.ErrorString(fmt.Sprintf("collect source of %s.", self.importName))
	}

	self.objectDir = dir
	obj := path.Join(self.objectDir, self.targetName+"."+arch)
	self.shouldUpdate = false
	if !self.ctx.fileExists(obj) {
		self.shouldUpdate = true
	} else {
		stat, error := os.Lstat(obj)
		if error != nil {
			return error
		}
		for _, src := range self.files {
			if stat.Mtime_ns < src.mtime_ns {
				self.shouldUpdate = true
				break
			}
		}
	}

	//if !self.shouldUpdate {
	//	return nil
	//}

	for _, src := range self.files {
	NEXT_IMPORT:
		for _, importName := range src.imports {
			for e := self.imports.Front(); e != nil; e = e.Next() {
				if e.Value.(*target).importName == importName {
					self.imports.Remove(e)
					self.imports.PushBack(e.Value.(*target))
					continue NEXT_IMPORT
				}
			}
			_, targetName := path.Split(path.Clean(importName))
			imp := newTarget(self.ctx, targetName, importName)
			if err := imp.reflesh(); err != nil {
				return err
			}
			if imp.isLocalPackage {
				self.imports.PushBack(imp)
				self.packagePaths["."]="."
			}
		}
	}

	return nil
}

func (self *target) build() ( bool, os.Error ) {
	for e := self.imports.Front(); e != nil; e = e.Next() {
		if done, err := e.Value.(*target).build(); err!=nil {
			return false, err
		} else if done {
			self.shouldUpdate = true
		}
	}
	if !self.shouldUpdate {
		return false, nil
	}
	if self.objectDir == "" {
		// TODO error
		return false, nil
	}
	// Compile
	i := 0
	argv := make([]string, 3)
	argv[0] = path.Join(gobin, arch+"g")
	argv[1] = "-o"
	argv[2] = path.Join(self.objectDir, self.targetName+"."+arch)
	includeArgs := make([]string, len(self.packagePaths)*2)
	linkArgs := make([]string, len(includeArgs))
	if len(includeArgs) > 0 {
		i = 0
		for _, pkgPath := range self.packagePaths {
			includeArgs[i] = "-I"
			includeArgs[i+1] = pkgPath
			linkArgs[i] = "-L"
			linkArgs[i+1] = pkgPath
			i+=2
		}
		argv = append(argv, includeArgs...)
	}
	fileArgs := make([]string, len(self.files))
	i = 0
	for _, src := range self.files {
		fileArgs[i] = src.filepath
		i++
	}
	argv = append(argv, fileArgs...)
	if err := self.ctx.exec(argv, "."); err != nil {
		return false, err
	}

	// Link/Pack
	if self.importName == "main" {
		argv = make([]string, 3)
		argv[0] = path.Join(gobin, arch+"l")
		argv[1] = "-o"
		argv[2] = path.Join(self.objectDir, self.targetName)
		if len(linkArgs) > 0 {
			argv = append(argv, linkArgs...)
		}
		argv = append(argv, path.Join(self.objectDir, self.targetName+"."+arch))
	} else {
		argv = make([]string, 4)
		argv[0] = path.Join(gobin, "gopack")
		argv[1] = "grc"
		argv[2] = path.Join(self.objectDir, self.targetName+".a")
		argv[3] = path.Join(self.objectDir, self.targetName+"."+arch)
	}

	if err := self.ctx.exec(argv, "."); err != nil {
		return false, err
	}

	self.shouldUpdate = false

	return true, nil
}

// context
type context struct {
	files       map[string]*source
	ignoreFiles map[string]string
}

func newContext() *context {
	c := new(context)
	c.files = make(map[string]*source)
	c.ignoreFiles = make(map[string]string)
	return c
}

func (self *context) getRunnableSource(filename string) (*source, os.Error) {

	temp := filename
	f, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	r := bufio.NewReader(f)

	var c byte

	if c, err = r.ReadByte(); err == os.EOF {
		return self.getSource(filename)
	} else if err != nil {
		return nil, err
	}

	if err = r.UnreadByte(); err != nil {
		return nil, err
	}

	// skip script header.
	commentCount := 0
	col := 0
	for {

		if c, err = r.ReadByte(); err == os.EOF {
			break
		} else if err != nil {
			return nil, err
		}

		if c == 0x0A { // LF
			// Unsupport CR(0x0D) EOL
			col = 0
			continue
		} else if c == 0x09 || c == 0x20 {
			// skip HT or SPACE
			continue
		}

		if col == 0 {

			if c== '#' {

				commentCount++

			} else if commentCount > 0 {

				if err = r.UnreadByte(); err != nil {
					return nil, err
				}
				break

			} else {
				// source without header
				return self.getSource(filename)
			}

		}
		col++
	}

	// write go source to temporary file.
	temp = filename + ".tmp"
	for i:=1 ;self.fileExists(temp); i++ {
		temp = fmt.Sprintf("%s.tmp%d", filename, i)
	}
	err = func() os.Error {
		tempFile, e := os.OpenFile(temp, os.O_WRONLY|os.O_CREATE, 0644)
		if e != nil {
			return e
		}
		defer func(){
			tempFile.Close()
			name := path.Clean(filename)
			self.ignoreFiles[name] = name
		}()

		w := bufio.NewWriter(tempFile)
		for {
			if c, e = r.ReadByte(); e == os.EOF {
				break
			} else if e != nil {
				return e
			}
			if e = w.WriteByte(c); e != nil {
				return e
			}
		}
		w.Flush()
		return nil
	}()
	if err != nil {
		return nil, err
	}

	src, err := self.getSource(temp)
	if err != nil {
		return nil, err
	}

	// Overwrite original Mtime_ns
	if stat, err := os.Lstat(filename); err != nil {
		return nil, err
	} else {
		src.mtime_ns = stat.Mtime_ns
	}

	return src, nil
}

func (self *context) getSource(filepath string) (*source, os.Error) {
	// TODO clean path
	filepath = path.Clean(filepath)

	// TODO to listDir
	if _, exist := self.ignoreFiles[filepath]; exist {
		return nil, nil
	}

	if src, exist := self.files[filepath]; exist {
		return src, nil
	}

	src, err := newSource(filepath)
	if err != nil {
		return nil, err
	}

	return src, nil
}

func (*context) fileExists(filename string) bool {

	file, err := os.Open(filename)
	defer file.Close()

	if patherr, ok := err.(*os.PathError); ok {
		return patherr.Error != os.ENOENT

	} else if err != nil {
		// Unknown
		return false
	}

	return true
}

func (*context) listDir(dirname string) []string {
	if file, err := os.Open(dirname); err == nil {
		defer file.Close()
		if fi, err := file.Readdir(-1); err == nil {
			//list := make( []string, 0 )
			//for i:=0; i<len(fi); i++ {
			//	list = append( list, fi[i].Name() )
			//}
			list := make([]string, len(fi))
			for i := 0; i < len(fi); i++ {
				_, filename := path.Split(fi[i].Name)
				list[i] = filename
			}
			return list
		}
	}
	return make([]string, 0)
}

func (*context) exec(args []string, dir string) os.Error {

	fmt.Println(strings.Join(args, " "))
	p, error := os.StartProcess(args[0], args,
		&os.ProcAttr{dir, os.Environ(), []*os.File{os.Stdin, os.Stdout, os.Stderr}})

	if error != nil {
		return error
	}

	if m, error := p.Wait(0); error != nil {
		return error
	} else if m.WaitStatus != 0 {
		return os.ErrorString(fmt.Sprintf("Status=%d", int(m.WaitStatus)))
	}

	return nil
}

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "go main-program [arg0 [arg1 ...]]")
		os.Exit(1)
	}

	ctx := newContext()

	targetName := args[0]
	if path.Ext(targetName) == ".go" {
		targetName = targetName[0 : len(targetName)-3]
	}

	// Build
	build := func() ( bool, os.Error ) {
		src, err := ctx.getRunnableSource(args[0])
		if err != nil { return false, err }

		// remove tmp file
		if src.filepath != args[0] {
			defer func(){
				if err = os.Remove(src.filepath); err != nil {
					// warn
					fmt.Fprintf(os.Stderr, "Can't %v\n", err)
				}
			}()
		}

		t := newTarget(ctx, targetName, src.packageName)
		t.files[src.filepath] = src
		t.ensureSources = true
		if err = t.reflesh(); err != nil { return false, err }

		return t.build()
	}
	
	if _, err := build(); err != nil {
		fmt.Fprintf(os.Stderr, "Can't %s\n", err)
		os.Exit(1)
	}

	// Run
	cmd := make([]string, 1)
	cmd[0] = targetName
	if targetName[0] != '.' {
		cmd[0] = "./"+targetName
	}
	cmd = append(cmd, args[1:]...)
	ctx.exec(cmd, ".")
}
