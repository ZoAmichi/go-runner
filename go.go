// Copyright 2009 Dimiter Stanev, malkia@gmail.com. All rights reserved.
// Copyright 2011 Kai Suzuki, kai.zoamichi@gmail.com. All rights reserved.
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
	"crypto/md5"
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
	gdb       = ""
	usage     = "go [-cdrCRNuEvV] [go-file] [args]"
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
	gdb = os.Getenv("GOGDB")
	if gdb == "" {
		gdb = "gdb"
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
	imports        *list.List // List<*target>
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
	t.shouldUpdate = false
	t.ensureSources = false
	t.isLocalPackage = true
	return t
}

func (self *target) reflesh() os.Error {
	//fmt.Println("reflesth: "+self.targetName)
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
		for _, f := range self.ctx.listFiles(path.Join(self.ctx.baseDir, dir)) {
			if f.IsDirectory() {continue}
			s := path.Join(self.ctx.baseDir, dir, f.Name)
			if path.Ext(s) != ".go" || strings.HasSuffix(s, "_test.go") {
				continue
			}
			if _, exist := self.files[s]; exist {
				continue
			}
			if src, err := self.ctx.getSource(s); err != nil {
				return err
			} else if src != nil && src.packageName == packageName {
				self.files[src.filepath] = src
			}
		}
	}

	if len(self.files) < 1 {
		return os.ErrorString(fmt.Sprintf("collect source of %s.", self.importName))
	}

	flag := self.ctx.flag
	if self.ctx.cacheDir != "" {

		if strings.HasPrefix(self.ctx.cacheDir, "./") {
			self.objectDir = path.Join(self.ctx.baseDir, dir)
			self.objectDir = path.Join(self.objectDir, self.ctx.cacheDir[2:], dir)
		} else {
			hash := md5.New()
			hash.Write( []byte( path.Join(self.ctx.baseDir, dir) ) )
			self.objectDir = path.Join( self.ctx.cacheDir, fmt.Sprintf( "%x", hash.Sum() ) )
			// TODO duplicate directory
		}

		if flag.debug {
			self.objectDir = path.Join( self.objectDir, "debug" )
		} else {
			self.objectDir = path.Join( self.objectDir, "release" )
		}

	} else {
		self.objectDir = path.Join(self.ctx.baseDir, dir)
	}

	
	obj := path.Join(self.objectDir, self.targetName+"."+arch)

	if !self.ctx.fileExists(self.objectDir) {
		if !flag.cleanOnly {
			if err := os.MkdirAll(self.objectDir, 0755); err != nil { return err }
		}
	}else if flag.cleanOnly || flag.rebuild {
		// clean
		targets := make([]string, 2)
		targets[0] = obj
		if self.importName == "main" {
			targets[1] = path.Join(self.objectDir, self.targetName)
		} else {
			targets[1] = path.Join(self.objectDir, self.targetName+".a")
		}
		for _, t := range targets {
			if err := os.Remove(t); err != nil {
				if patherr, ok := err.(*os.PathError); ok {
					if patherr.Error == os.ENOENT {continue}
				}
				// warn
				fmt.Fprintf(os.Stderr, "Can't %v\n", err)
			}
		}
	}

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

	flag := self.ctx.flag

	// Compile
	i := 0
	argv := make([]string, 1)
	argv[0] = path.Join(gobin, arch+"g")
	if flag.disableOptimiz || flag.debug {
		argv = append( argv, "-N")
	}
	if flag.disallowUnsafe {
		argv = append( argv, "-u")
	}
	argv = append( argv, []string{ "-o", path.Join(self.objectDir, self.targetName+"."+arch) }... )
	includeArgs := make([]string, self.imports.Len()*2)
	linkArgs := make([]string, len(includeArgs))
	if len(includeArgs) > 0 {
		i = 0
		for e:=self.imports.Front(); e!=nil; e=e.Next() {
			t := e.Value.(*target)
			includeArgs[i] = "-I"
			includeArgs[i+1] = t.objectDir
			linkArgs[i] = "-L"
			linkArgs[i+1] = t.objectDir
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
	cmdLink := make([]string, 1)
	cmdLink[0] = path.Join(gobin, arch+"l")
	if flag.extraSymbol || flag.debug {
		cmdLink = append( cmdLink, "-e")
	}
	cmdLink = append( cmdLink, "-o")
	if self.importName == "main" {
		argv = cmdLink
		argv = append( argv, path.Join(self.objectDir, self.targetName) )
		if len(linkArgs) > 0 {
			argv = append(argv, linkArgs...)
		}
		argv = append(argv, path.Join(self.objectDir, self.targetName+"."+arch))
	} else {
		argv = []string {
			path.Join(gobin, "gopack"),
			"grc",
			path.Join(self.objectDir, self.targetName+".a"),
			path.Join(self.objectDir, self.targetName+"."+arch),
		}
	}

	if err := self.ctx.exec(argv, "."); err != nil {
		return false, err
	}

	self.shouldUpdate = false

	return true, nil
}

func run(t *target) (int, os.Error) {
	cmd := make([]string, 1)
	cmd[0] = path.Join(t.objectDir, t.targetName)
	cmd = append(cmd, os.Args[t.ctx.nArg:]...)

	//fmt.Println(strings.Join(cmd, " "))
	p, err := os.StartProcess(cmd[0], cmd,
		&os.ProcAttr{".", os.Environ(), []*os.File{os.Stdin, os.Stdout, os.Stderr}})

	if err != nil {
		return 1, err
	}

	if m, err := p.Wait(0); err != nil {
		return 1, err
	} else if m.WaitStatus != 0 {
		return int(m.WaitStatus), nil
	}
	
	return 0, nil
}

func runDebugger(t *target) (int, os.Error) {
	cmd := make([]string, 3)
	if c, exist := t.ctx.whereIs(gdb); !exist {
		return 1, os.ErrorString("find gdb.")
	} else {
		cmd[0] = c
	}
	cmd[1] = "--args"
	cmd[2] = path.Join(t.objectDir, t.targetName)
	cmd = append(cmd, os.Args[t.ctx.nArg:]...)

	//fmt.Println(strings.Join(cmd," "))
	p, err := os.StartProcess(cmd[0], cmd,
		&os.ProcAttr{".", os.Environ(), []*os.File{os.Stdin, os.Stdout, os.Stderr}})
	if err != nil {
		return 1, err
	}

	if m, err := p.Wait(0); err != nil {
		return 1, err
	} else if m.WaitStatus != 0 {
		return int(m.WaitStatus), nil
	}

	return 0, nil
}

// flag
type flag struct {
	debug bool
	encache bool
	cleanOnly bool
	rebuild bool
	norun bool
	disableOptimiz bool
	disallowUnsafe bool
	extraSymbol bool
	verbose bool
	version bool
}

// context
type context struct {
	flag        *flag
	nArg        int
	baseDir     string
	cacheDir    string
	gofile      string
	path        []string
	files       map[string]*source
	ignoreFiles map[string]string
}

func newContext() (*context, os.Error) {
	c := new(context)
	c.flag = new(flag)
	c.files = make(map[string]*source)
	c.ignoreFiles = make(map[string]string)
	c.gofile = ""
	c.path = strings.Split(os.Getenv("PATH"), ":", -1)
	c.nArg = 1

	flagMap := map[int]*bool {
		'c':&c.flag.encache,
		'd':&c.flag.debug,
		'r':&c.flag.rebuild,
		'C':&c.flag.cleanOnly,
		'N':&c.flag.disableOptimiz,
		'R':&c.flag.norun,
		'u':&c.flag.disallowUnsafe,
		'E':&c.flag.extraSymbol,
		'v':&c.flag.verbose,
		'V':&c.flag.version,
	}

	// Parse Arguments.
	for _, arg := range os.Args[1:] {

		c.nArg++
		if len(arg) < 2 { continue }
		if arg[0] == '-' {

			// flags
			for _, c := range arg[1:] {
				if ref, exist := flagMap[c]; exist {
					*ref = true
				} else {
					return nil, os.ErrorString(fmt.Sprintf("Unknown option: -%c", c))
				}
			}

		} else {
			// source
			c.baseDir, c.gofile = path.Split(arg)
			c.baseDir = path.Clean(c.baseDir)
			break
		}

	}

	// cache directory
	c.cacheDir = os.Getenv("GOCACHE")
	if c.cacheDir == "" && c.flag.encache {
		c.cacheDir = "."
	}

	if c.cacheDir == "." {
		c.cacheDir = "./.go"
	} else if c.cacheDir != "" {
		c.cacheDir = path.Clean(c.cacheDir)
		if c.cacheDir[0] == '~' {
			c.cacheDir = path.Join(os.Getenv("HOME"), c.cacheDir[1:])
		}
		// TODO check
	}

	return c, nil
}

func (self *context) getRunnableSource(filename string) (*source, os.Error) {
	
	filename = path.Join(self.baseDir, filename)
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
	tempPath := filename+".tmp"
	for i:=1 ;self.fileExists(tempPath); i++ {
		tempPath = fmt.Sprintf("%s.%d", filename, i)
	}
	err = func() os.Error {
		f, e := os.OpenFile(tempPath, os.O_WRONLY|os.O_CREATE, 0644)
		if e != nil {
			return e
		}
		defer func(){
			f.Close()
			name := path.Clean(filename)
			self.ignoreFiles[name] = name
		}()

		w := bufio.NewWriter(f)
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

	src, err := self.getSource(tempPath)
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

	// TODO to listFiles
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

func (self *context) whereIs(name string) (cmd string, exist bool) {
	// TODO ./
	// TODO ~/
	for _,dir := range self.path {
		cmd = path.Join(dir, name)
		if self.fileExists(cmd) {
			exist = true
			return
		}
	}
	cmd = ""
	exist = false
	return
}

func (self *context) fileExists(filename string) bool {

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

func (self *context) listFiles(dirname string) []os.FileInfo {
	if file, err := os.Open(dirname); err == nil {
		defer file.Close()
		if fi, err := file.Readdir(-1); err == nil {
			return fi
		}
	}
	return make([]os.FileInfo, 0)
}

func (*context) exec(args []string, dir string) os.Error {

	//fmt.Println(strings.Join(args, " "))
	p, error := os.StartProcess(args[0], args,
		&os.ProcAttr{dir, os.Environ(), []*os.File{nil, os.Stdout, os.Stderr}})

	if error != nil {
		return error
	}

	if m, error := p.Wait(0); error != nil {
		return error
	} else if m.WaitStatus != 0 {
		return os.ErrorString(fmt.Sprintf("%s Exit(%d)", args[0], int(m.WaitStatus)))
	}

	return nil
}

func main() {

	ctx, err := newContext()

	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		fmt.Println(usage)
		os.Exit(1)
	}

	if ctx.gofile == "" {
		fmt.Println(usage)
		os.Exit(1)
	}

	targetName := ctx.gofile
	if path.Ext(targetName) == ".go" {
		targetName = targetName[0 : len(targetName)-3]
	}

	// Build
	var t *target
	builder := func() ( bool, os.Error ) {
		src, err := ctx.getRunnableSource(ctx.gofile)
		if err != nil { return false, err }

		// remove tmp file
		if src.filepath != ctx.gofile {
			defer func(){
				if err = os.Remove(src.filepath); err != nil {
					// warn
					fmt.Fprintf(os.Stderr, "Can't %v\n", err)
				}
			}()
		}

		t = newTarget(ctx, targetName, src.packageName)
		t.files[src.filepath] = src
		t.ensureSources = true
		if err = t.reflesh(); err != nil { return false, err }

		if !ctx.flag.cleanOnly {
			return t.build()
		}

		return false, nil
	}
	
	if _, err := builder(); err != nil {
		fmt.Fprintf(os.Stderr, "Can't %s\n", err)
		os.Exit(1)
	}

	if ctx.flag.norun || ctx.flag.cleanOnly {
		os.Exit(0)
	}

	var runner func(*target) (int, os.Error)
	if ctx.flag.debug {
		runner = runDebugger
	} else {
		runner = run
	}

	status, err := runner(t)
	if err!=nil {
		fmt.Fprintf(os.Stderr, "Can't %s\n", err)
	}
	os.Exit(status)
}
