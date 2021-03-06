package internal

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/ioutil"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

func isDir(filename string) bool {
	fi, err := os.Stat(filename)
	return err == nil && fi.IsDir()
}

func Do(params CmdParams, paths []string) []FunctionStats {
	var stats []FunctionStats
	for _, path := range paths {
		if strings.HasSuffix(path, "/...") {
			stats = append(stats, analyzeDirRecursively(params, path[:len(path)-3])...)
		} else if isDir(path) {
			stats = append(stats, analyzeDir(params, path)...)
		} else {
			stats = append(stats, analyzeFile(params, path)...)
		}
	}

	l := FunctionStatsList{
		SortType: params.Types[0],
		Stats:    stats,
	}
	sort.Sort(l)

	return l.Stats
}

func analyzeFile(params CmdParams, fname string) []FunctionStats {
	stats := []FunctionStats{}

	if !strings.HasSuffix(fname, ".go") {
		return stats
	}

	if params.Ignore != nil && params.Ignore.MatchString(fname) {
		//fmt.Println("Ignored file", fname)
		return stats
	}

	isTest := strings.HasSuffix(fname, "_test.go")
	//fmt.Println(params.IncludeTests, isTest, fname)
	if isTest && !params.IncludeTests {
		return stats
	}

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, fname, nil, parser.ParseComments)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing %s: %s\n", fname, err.Error())
		return stats
	}

	//fmt.Println("file=", "pos=", f.Pos())
	v := NewVisitor(params, f, fset, stats)
	ast.Walk(v, f)

	return v.stats
}

func analyzeDir(params CmdParams, dirname string) []FunctionStats {
	finfos, err := ioutil.ReadDir(dirname)
	if err != nil {
		PrintUsage("Error reading %s: %s", dirname, err.Error())
	}

	stats := []FunctionStats{}
	for _, fi := range finfos {
		stats = append(stats, analyzeFile(params, path.Join(dirname, fi.Name()))...)
	}

	return stats
}

func analyzeDirRecursively(params CmdParams, dirname string) []FunctionStats {
	stats := []FunctionStats{}

	err := filepath.Walk(dirname, func(path string, info os.FileInfo, err error) error {
		if !params.IncludeVendor {
			if strings.Contains(path, "vendor") { // TODO
				return err
			}
		}
		if err == nil && !info.IsDir() && strings.HasSuffix(path, ".go") {
			stats = append(stats, analyzeFile(params, path)...)
		}
		return err
	})
	if err != nil {
		PrintUsage("Error walking through files %s", err.Error())
	}
	return stats
}

type Visitor struct {
	file     *ast.File
	contents string
	fset     *token.FileSet
	offset   int
	stats    []FunctionStats
	params   CmdParams
}

func NewVisitor(params CmdParams, file *ast.File, fset *token.FileSet, stats []FunctionStats) *Visitor {
	v := Visitor{
		file:   file,
		fset:   fset,
		offset: int(file.Pos()),
		stats:  stats,
		params: params,
	}

	f := fset.File(file.Pos())
	if f == nil {
		panic("No file found for " + f.Name())
	}

	bytes, err := ioutil.ReadFile(f.Name())
	if err != nil {
		PrintUsage("Error reading %s: %s", f.Name(), err.Error())
	}

	v.contents = string(bytes)

	return &v
}

func (v *Visitor) Visit(node ast.Node) ast.Visitor {
	if node == nil {
		return v
	}
	switch n := node.(type) {
	case *ast.FuncDecl:
		fun := n
		if v.params.IgnoreFuncs != nil {
			if v.params.IgnoreFuncs.MatchString(fun.Name.Name) {
				return v
			}
		}
		stats := newFunctionStats(fun.Name.Name, v.fset.Position(fun.Pos()).String())
		v.params.Printf("Visiting %s in %s", fun.Name.Name, stats.Location)
		if fun.Recv != nil && len(fun.Recv.List) > 0 {
			ty := fun.Recv.List[0].Type
			if st, is := ty.(*ast.StarExpr); is {
				stats.Receiver = fmt.Sprintf("*%v", st.X)
			} else {
				stats.Receiver = fmt.Sprintf("%v", ty)
			}
		}
		var functionDocs string
		if n.Doc != nil {
			functionDocs = n.Doc.Text()
		}
		calculateLines(stats, v.offset, fun, v.contents, v.file.Comments, functionDocs)
		calculateComplexity(stats, fun)
		calculateNesting(stats, v.offset, fun, v.contents)
		calculateVariables(stats, fun)
		v.stats = append(v.stats, *stats)
		//fmt.Printf("stats=%d\n", len(v.stats))
	}
	return v
}
