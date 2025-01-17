package syntax

import (
	"cmd/compile/internal/base"
	"cmd/compile/internal/syntax"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	xgo_ctxt "cmd/compile/internal/xgo_rewrite_internal/patch/ctxt"
	xgo_func_name "cmd/compile/internal/xgo_rewrite_internal/patch/func_name"
)

const XGO_TOOLCHAIN_VERSION = "XGO_TOOLCHAIN_VERSION"
const XGO_TOOLCHAIN_REVISION = "XGO_TOOLCHAIN_REVISION"
const XGO_TOOLCHAIN_VERSION_NUMBER = "XGO_TOOLCHAIN_VERSION_NUMBER"

const XGO_VERSION = "XGO_VERSION"
const XGO_REVISION = "XGO_REVISION"
const XGO_NUMBER = "XGO_NUMBER"

// this link function is considered safe as we do not allow user
// to define such one,there will no abuse
const XgoLinkGeneratedRegisterFunc = "__xgo_link_generated_register_func"
const XgoRegisterFuncs = "__xgo_register_funcs"
const XgoLocalFuncStub = "__xgo_local_func_stub"
const XgoLocalPkgName = "__xgo_local_pkg_name"

const sig_expected__xgo_register_func = `func(info interface{})`

func init() {
	if sig_gen__xgo_register_func != sig_expected__xgo_register_func {
		panic(fmt.Errorf("__xgo_register_func signature changed, run go generate and update sig_expected__xgo_register_func correspondly"))
	}
}

func AfterFilesParsed(fileList []*syntax.File, addFile func(name string, r io.Reader) *syntax.File) {
	debugSyntax(fileList)
	patchVersions(fileList)
	fillFuncArgResNames(fileList)
	registerFuncs(fileList, addFile)
}

// typeinfo not used
// func AfterSyntaxTypeCheck(pkgPath string, files []*syntax.File, info *types2.Info) {
// 	if pkgPath != "github.com/xhd2015/xgo/runtime/test/debug" {
// 		return
// 	}
// 	if true {
// 		return
// 	}
// 	stmt := files[0].DeclList[2].(*syntax.FuncDecl).Body.List[0]
// 	call := stmt.(*syntax.ExprStmt).X.(*syntax.CallExpr)
// 	name := call.ArgList[0].(*syntax.Name)
// 	if false {
// 		v := &syntax.BasicLit{Value: "11", Kind: syntax.IntLit}
// 		t := syntax.TypeAndValue{
// 			Type:  name.GetTypeInfo().Type,
// 			Value: constant.MakeInt64(11),
// 		}
// 		t.SetIsValue()
// 		v.SetTypeInfo(t)
// 		call.ArgList[0] = v
// 	}

// 	_ = name
// }

func debugPkgSyntax(files []*syntax.File) {
	if false {
		return
	}
	pkgPath := xgo_ctxt.GetPkgPath()
	if pkgPath != "github.com/xhd2015/xgo/runtime/test/debug" {
		return
	}

	stmt := files[0].DeclList[2].(*syntax.FuncDecl).Body.List[1]
	call := stmt.(*syntax.ExprStmt).X.(*syntax.CallExpr)
	name := call.ArgList[0].(*syntax.Name)
	// if false {
	call.ArgList[0] = &syntax.XgoSimpleConvert{
		X: &syntax.CallExpr{
			Fun: syntax.NewName(name.Pos(), "int"),
			ArgList: []syntax.Expr{
				name,
			},
		},
	}
	// }
}

func GetSyntaxDeclMapping() map[string]map[LineCol]*DeclInfo {
	return getSyntaxDeclMapping()
}

var allFiles []*syntax.File
var allDecls []*DeclInfo

func ClearFiles() {
	allFiles = nil
}

// not used anywhere
func GetFiles() []*syntax.File {
	return allFiles
}

func GetDecls() []*DeclInfo {
	return allDecls
}

func ClearDecls() {
	allDecls = nil
}

type LineCol struct {
	Line uint
	Col  uint
}

var syntaxDeclMapping map[string]map[LineCol]*DeclInfo

func getSyntaxDeclMapping() map[string]map[LineCol]*DeclInfo {
	if syntaxDeclMapping != nil {
		return syntaxDeclMapping
	}
	// build pos -> syntax mapping
	syntaxDeclMapping = make(map[string]map[LineCol]*DeclInfo)
	for _, syntaxDecl := range allDecls {
		if syntaxDecl.Interface {
			continue
		}
		if !syntaxDecl.Kind.IsFunc() {
			continue
		}
		file := syntaxDecl.File
		fileMapping := syntaxDeclMapping[file]
		if fileMapping == nil {
			fileMapping = make(map[LineCol]*DeclInfo)
			syntaxDeclMapping[file] = fileMapping
		}
		fileMapping[LineCol{
			Line: uint(syntaxDecl.Line),
			Col:  syntaxDecl.FuncDecl.Pos().Col(),
		}] = syntaxDecl
	}
	return syntaxDeclMapping
}

var computedSkipTrap bool
var skipTrap bool

func HasSkipTrap() bool {
	if computedSkipTrap {
		return skipTrap
	}
	computedSkipTrap = true
	skipTrap = computeSkipTrap(allFiles)
	return skipTrap
}

func computeSkipTrap(files []*syntax.File) bool {
	// check if any file has __XGO_SKIP_TRAP
	for _, f := range files {
		for _, d := range f.DeclList {
			if d, ok := d.(*syntax.ConstDecl); ok && len(d.NameList) > 0 && d.NameList[0].Value == "__XGO_SKIP_TRAP" {
				return true
			}
		}
	}
	return false
}

func ClearSyntaxDeclMapping() {
	syntaxDeclMapping = nil
}

func registerFuncs(fileList []*syntax.File, addFile func(name string, r io.Reader) *syntax.File) {
	allFiles = fileList
	if xgo_ctxt.SkipPackageTrap() {
		return
	}
	var pkgName string
	pkgPath := xgo_ctxt.GetPkgPath()
	if len(fileList) > 0 {
		pkgName = fileList[0].PkgName.Value
	}

	// debugPkgSyntax(fileList)
	// if true {
	// 	return
	// }
	// cannot directly import the runtime package
	// but we can first:
	//  1.modify the importcfg
	//  2.do not import anything, rely on IR to finish remaining steps
	//
	// I feel the second is more proper as importcfg is an extra layer of
	// complexity, and runtime can be compiled or cached, we cannot locate
	// where its _pkg_.a is.

	varTrap := allowVarTrap()

	funcDelcs := getFuncDecls(fileList, varTrap)
	for _, funcDecl := range funcDelcs {
		if funcDecl.RecvTypeName == "" && funcDecl.Name == XgoLinkGeneratedRegisterFunc {
			// ensure we are safe
			// refuse to link such volatile package
			return
		}
	}
	// filterFuncDecls
	funcDelcs = filterFuncDecls(funcDelcs, pkgPath)
	// assign to global
	allDecls = funcDelcs

	// std lib functions
	rewriteStdAndGenericFuncs(funcDelcs, pkgPath)

	if varTrap {
		trapVariables(pkgPath, fileList, funcDelcs)
		// debug
		// fmt.Fprintf(os.Stderr, "ast:")
		// syntax.Fdump(os.Stderr, fileList[0])
	}

	// always generate a helper to aid IR
	helperFile := addFile("__xgo_autogen_register_func_helper.go", strings.NewReader(generateRegHelperCode(pkgName)))

	// change __xgo_local_pkg_name
	for _, decl := range helperFile.DeclList {
		if constDecl, ok := decl.(*syntax.ConstDecl); ok && constDecl.NameList[0].Value == XgoLocalPkgName {
			constDecl.Values.(*syntax.BasicLit).Value = strconv.Quote(pkgPath)
			break
		}
	}
	// split fileDecls to a list of batch
	// when statements gets large, it will
	// exceeds the compiler's threshold, causing
	//     internal compiler error: NewBulk too big
	// see https://github.com/golang/go/issues/33437
	// see also: https://github.com/golang/go/issues/57832 The input code is just too big for the compiler to handle.
	// here we split the files per 1000 functions
	batchFuncDecls := splitBatch(funcDelcs, 1000)
	for i, funcDecls := range batchFuncDecls {
		if len(funcDecls) == 0 {
			continue
		}

		// use XgoLinkRegFunc for general purepose
		body := generateFuncRegBody(funcDecls, XgoLinkGeneratedRegisterFunc, XgoLocalFuncStub)

		// NOTE: here the trick is to use init across multiple files,
		// in go, init can appear more than once even in single file
		fileCode := generateRegFileCode(pkgName, "init", body)
		fileNameBase := "__xgo_autogen_register_func_info"
		if len(batchFuncDecls) > 1 {
			// special when there are multiple files
			fileNameBase += fmt.Sprintf("_%d", i)
		}
		addFile(fileNameBase+".go", strings.NewReader(fileCode))
		if false && pkgPath == "main" {
			// debug
			os.WriteFile("/tmp/debug.go", []byte(fileCode), 0755)
			panic("debug")
		}
	}
}

func patchVersions(fileList []*syntax.File) {
	pkgPath := xgo_ctxt.GetPkgPath()
	if pkgPath != xgo_ctxt.XgoRuntimeCorePkg {
		return
	}
	version := os.Getenv(XGO_TOOLCHAIN_VERSION)
	if version == "" {
		return
	}
	revision := os.Getenv(XGO_TOOLCHAIN_REVISION)
	if revision == "" {
		return
	}
	versionNumEnv := os.Getenv(XGO_TOOLCHAIN_VERSION_NUMBER)
	versionNum, err := strconv.ParseInt(versionNumEnv, 10, 64)
	if err != nil || versionNum <= 0 {
		return
	}

	var versionFile *syntax.File
	for _, file := range fileList {
		if strings.HasSuffix(file.Pos().RelFilename(), "version.go") {
			versionFile = file
			break
		}
	}
	if versionFile == nil {
		return
	}
	for _, decl := range versionFile.DeclList {
		constDecl, ok := decl.(*syntax.ConstDecl)
		if !ok {
			continue
		}

		for _, name := range constDecl.NameList {
			switch name.Value {
			case XGO_VERSION:
				constDecl.Values = newStringLit(version)
			case XGO_REVISION:
				constDecl.Values = newStringLit(revision)
			case XGO_NUMBER:
				constDecl.Values = newIntLit(int(versionNum))
			}
		}
	}
}

func getFileIndexMapping(files []*syntax.File) map[*syntax.File]int {
	m := make(map[*syntax.File]int, len(files))
	for i, file := range files {
		m[file] = i
	}
	return m
}
func splitBatch(funcDecls []*DeclInfo, batch int) [][]*DeclInfo {
	if batch <= 0 {
		panic("invalid batch")
	}
	var res [][]*DeclInfo

	var cur []*DeclInfo
	n := len(funcDecls)
	for i := 0; i < n; i++ {
		cur = append(cur, funcDecls[i])
		if len(cur) >= batch {
			res = append(res, cur)
			cur = nil
		}
	}
	if len(cur) > 0 {
		res = append(res, cur)
		cur = nil
	}
	return res
}

type FileDecl struct {
	File  *syntax.File
	Funcs []*DeclInfo
}
type DeclKind int

const (
	Kind_Func   DeclKind = 0
	Kind_Var    DeclKind = 1
	Kind_VarPtr DeclKind = 2
	Kind_Const  DeclKind = 3

	// TODO
	// Kind_Interface VarKind = 4
)

func (c DeclKind) IsFunc() bool {
	return c == Kind_Func
}

func (c DeclKind) IsVarOrConst() bool {
	return c == Kind_Var || c == Kind_VarPtr || c == Kind_Const
}

type DeclInfo struct {
	FuncDecl  *syntax.FuncDecl
	VarDecl   *syntax.VarDecl
	ConstDecl *syntax.ConstDecl

	// is this var decl follow a const __xgo_trap_xxx = 1?
	FollowingTrapConst bool

	Kind         DeclKind
	Name         string
	RecvTypeName string
	RecvPtr      bool
	Generic      bool
	Closure      bool

	// this is an interface type declare
	// only the RecvTypeName is valid
	Interface bool

	// arg names
	RecvName     string
	ArgNames     []string
	ResNames     []string
	FirstArgCtx  bool
	LastResError bool

	FileSyntax *syntax.File
	FileIndex  int
	File       string
	FileRef    string
	Line       int
}

func (c *DeclInfo) RefName() string {
	if c.Interface {
		return "nil"
	}
	// if c.Generic, then the ref name is for generic
	if !c.Kind.IsFunc() {
		return c.Name
	}
	return xgo_func_name.FormatFuncRefName(c.RecvTypeName, c.RecvPtr, c.Name)
}

func (c *DeclInfo) GenericName() string {
	if !c.Generic {
		return ""
	}
	return c.RefName()
}

func (c *DeclInfo) IdentityName() string {
	if c.Interface {
		return c.RecvTypeName
	}
	if !c.Kind.IsFunc() {
		if c.Kind == Kind_VarPtr {
			return "*" + c.Name
		}
		return c.Name
	}
	return xgo_func_name.FormatFuncRefName(c.RecvTypeName, c.RecvPtr, c.Name)
}

func fillMissingArgNames(fn *syntax.FuncDecl) {
	if fn.Recv != nil {
		fillName(fn.Recv, "__xgo_recv_auto_filled")
	}
	for i, p := range fn.Type.ParamList {
		fillName(p, fmt.Sprintf("__xgo_arg_auto_filled_%d", i))
	}
}

func fillName(field *syntax.Field, namePrefix string) {
	if field.Name == nil {
		field.Name = syntax.NewName(field.Pos(), namePrefix)
		return
	}
	if field.Name.Value == "_" {
		field.Name.Value = namePrefix + "_blank"
		return
	}
}

// collect funcs from files, register each of them by
// calling to __xgo_reg_func with names and func pointer

func getFuncDecls(files []*syntax.File, varTrap bool) []*DeclInfo {
	// fileInfos := make([]*FileDecl, 0, len(files))
	var declFuncs []*DeclInfo
	for i, f := range files {
		file := f.Pos().RelFilename()
		for _, decl := range f.DeclList {
			fnDecls := extractFuncDecls(i, f, file, decl, varTrap)
			declFuncs = append(declFuncs, fnDecls...)
		}
	}
	// compute __xgo_trap_xxx
	n := len(declFuncs)
	if n == 0 {
		return nil
	}
	j := n
	for i := n - 1; i >= 0; i-- {
		if i == 0 || !isTrapped(declFuncs, i) {
			j--
			declFuncs[j] = declFuncs[i]
			continue
		}
		// a special comment
		declFuncs[i].FollowingTrapConst = true
		j--
		declFuncs[j] = declFuncs[i]
		// remove the comment by skipping next
		i--
	}
	return declFuncs[j:]
}

func isTrapped(declFuncs []*DeclInfo, i int) bool {
	fn := declFuncs[i]
	if fn.Kind != Kind_Var {
		return false
	}
	last := declFuncs[i-1]
	if last.Kind != Kind_Const {
		return false
	}
	const xgoTrapPrefix = "__xgo_trap_"
	if !strings.HasPrefix(last.Name, xgoTrapPrefix) {
		return false
	}
	subName := last.Name[len(xgoTrapPrefix):]
	if !strings.EqualFold(fn.Name, subName) {
		return false
	}
	return true
}

func filterFuncDecls(funcDecls []*DeclInfo, pkgPath string) []*DeclInfo {
	filtered := make([]*DeclInfo, 0, len(funcDecls))
	for _, fn := range funcDecls {
		// disable part of stdlibs
		if !xgo_ctxt.AllowPkgFuncTrap(pkgPath, base.Flag.Std, fn.IdentityName()) {
			continue
		}
		filtered = append(filtered, fn)
	}
	return filtered
}

func extractFuncDecls(fileIndex int, f *syntax.File, file string, decl syntax.Decl, varTrap bool) []*DeclInfo {
	switch decl := decl.(type) {
	case *syntax.FuncDecl:
		info := getFuncDeclInfo(fileIndex, f, file, decl)
		if info == nil {
			return nil
		}
		return []*DeclInfo{info}
	case *syntax.VarDecl:
		if !varTrap {
			return nil
		}
		varDecls := collectVarDecls(Kind_Var, decl.NameList, decl.Type)
		for _, varDecl := range varDecls {
			varDecl.VarDecl = decl

			varDecl.FileSyntax = f
			varDecl.FileIndex = fileIndex
			varDecl.File = file
		}
		return varDecls
	case *syntax.ConstDecl:
		if !varTrap {
			return nil
		}
		constDecls := collectVarDecls(Kind_Const, decl.NameList, decl.Type)
		for _, constDecl := range constDecls {
			constDecl.ConstDecl = decl

			constDecl.FileSyntax = f
			constDecl.FileIndex = fileIndex
			constDecl.File = file
		}
		return constDecls
	case *syntax.TypeDecl:
		if decl.Alias {
			return nil
		}
		// TODO: test generic interface
		if len(decl.TParamList) > 0 {
			return nil
		}

		// NOTE: for interface type, we only set a marker
		// because we cannot handle Embed interface if
		// the that comes from other package
		if _, ok := decl.Type.(*syntax.InterfaceType); ok {
			return []*DeclInfo{
				&DeclInfo{
					RecvTypeName: decl.Name.Value,
					Interface:    true,

					FileSyntax: f,
					FileIndex:  fileIndex,
					File:       file,
					Line:       int(decl.Pos().Line()),
				},
			}
		}
	}
	return nil
}

func getFuncDeclInfo(fileIndex int, f *syntax.File, file string, fn *syntax.FuncDecl) *DeclInfo {
	line := fn.Pos().Line()
	if fn.Name.Value == "init" {
		return nil
	}
	var genericFunc bool
	if len(fn.TParamList) > 0 {
		genericFunc = true
	}
	var recvTypeName string
	var recvPtr bool
	var recvName string
	var genericRecv bool
	fillMissingArgNames(fn)
	if fn.Recv != nil {
		recvName = "_"
		if fn.Recv.Name != nil {
			recvName = fn.Recv.Name.Value
		}

		recvTypeExpr := fn.Recv.Type

		// *A
		if starExpr, ok := fn.Recv.Type.(*syntax.Operation); ok && starExpr.Op == syntax.Mul {
			// *A
			recvTypeExpr = starExpr.X
			recvPtr = true
		}
		// check if generic
		if indexExpr, ok := recvTypeExpr.(*syntax.IndexExpr); ok {
			// *A[T] or A[T]
			// the generic receiver
			// currently not handled
			genericRecv = true
			recvTypeExpr = indexExpr.X
		}

		recvTypeName = recvTypeExpr.(*syntax.Name).Value
	}
	var firstArgCtx bool
	var lastResErr bool
	if false {
		// NOTE: these fields will be retrieved at runtime dynamically
		if len(fn.Type.ParamList) > 0 && hasQualifiedName(fn.Type.ParamList[0].Type, "context", "Context") {
			firstArgCtx = true
		}
		if len(fn.Type.ResultList) > 0 && isName(fn.Type.ResultList[len(fn.Type.ResultList)-1].Type, "error") {
			lastResErr = true
		}
	}

	return &DeclInfo{
		FuncDecl:     fn,
		Name:         fn.Name.Value,
		RecvTypeName: recvTypeName,
		RecvPtr:      recvPtr,
		Generic:      genericFunc || genericRecv,

		RecvName: recvName,
		ArgNames: getFieldNames(fn.Type.ParamList),
		ResNames: getFieldNames(fn.Type.ResultList),

		FirstArgCtx:  firstArgCtx,
		LastResError: lastResErr,

		FileSyntax: f,
		FileIndex:  fileIndex,
		File:       file,
		Line:       int(line),
	}
}

func getFileRef(i int) string {
	return fmt.Sprintf("__xgo_reg_file_gen_%d", i)
}

func gen() string {
	return ""
}

type StructDef struct {
	Field string
	Type  string
}

func genStructType(fields []*StructDef) string {
	concats := make([]string, 0, len(fields))
	for _, field := range fields {
		concats = append(concats, fmt.Sprintf("%s %s", field.Field, field.Type))
	}
	return fmt.Sprintf("struct{\n%s\n}\n", strings.Join(concats, "\n"))
}

func generateFuncRegBody(funcDecls []*DeclInfo, xgoRegFunc string, xgoLocalFuncStub string) string {
	fileDeclaredMapping := make(map[int]bool)
	var fileDefs []string
	stmts := make([]string, 0, len(funcDecls))
	for _, funcDecl := range funcDecls {
		if funcDecl.Name == "_" {
			// there are function with name "_"
			continue
		}
		var fnRefName string = "nil"
		var varRefName string = "nil"
		if funcDecl.Kind.IsFunc() {
			if !funcDecl.Generic {
				fnRefName = funcDecl.RefName()
			}
		} else if funcDecl.Kind == Kind_Var {
			varRefName = "&" + funcDecl.RefName()
		} else if funcDecl.Kind == Kind_Const {
			varRefName = funcDecl.RefName()
		}
		fileIdx := funcDecl.FileIndex
		fileRef := getFileRef(fileIdx)
		// func(pkgPath string, fn interface{}, recvTypeName string, recvPtr bool, name string, identityName string, generic bool, recvName string, argNames []string, resNames []string, firstArgCtx bool, lastResErr bool, file string, line int)

		// check __xgo_local_func_stub for correctness
		regKind := func(kind DeclKind, identityName string) {
			fieldList := []string{
				XgoLocalPkgName,                           // PkgPath
				strconv.FormatInt(int64(kind), 10),        // Kind
				fnRefName,                                 // Fn
				varRefName,                                // Var
				"0",                                       // PC, filled later
				strconv.FormatBool(funcDecl.Interface),    // Interface
				strconv.FormatBool(funcDecl.Generic),      // Generic
				strconv.FormatBool(funcDecl.Closure),      // Closure
				strconv.Quote(funcDecl.RecvTypeName),      // RecvTypeName
				strconv.FormatBool(funcDecl.RecvPtr),      // RecvPtr
				strconv.Quote(funcDecl.Name),              // Name
				strconv.Quote(identityName),               // IdentityName
				strconv.Quote(funcDecl.RecvName),          // RecvName
				quoteNamesExpr(funcDecl.ArgNames),         // ArgNames
				quoteNamesExpr(funcDecl.ResNames),         // ResNames
				strconv.FormatBool(funcDecl.FirstArgCtx),  // FirstArgCtx
				strconv.FormatBool(funcDecl.LastResError), // LastResErr
				fileRef, /* declFunc.FileRef */ // File
				strconv.FormatInt(int64(funcDecl.Line), 10), // Line
			}
			fields := strings.Join(fieldList, ",")
			stmts = append(stmts, fmt.Sprintf("%s(%s{%s})", xgoRegFunc, xgoLocalFuncStub, fields))
		}
		identityName := funcDecl.IdentityName()
		regKind(funcDecl.Kind, identityName)
		if funcDecl.Kind == Kind_Var {
			regKind(Kind_VarPtr, "*"+identityName)
		}

		// add files
		if !fileDeclaredMapping[fileIdx] {
			fileDeclaredMapping[fileIdx] = true
			fileValue := funcDecl.FileSyntax.Pos().RelFilename()
			fileDefs = append(fileDefs, fmt.Sprintf("%s := %q", fileRef, fileValue))
		}
	}

	if len(stmts) == 0 {
		return ""
	}
	allStmts := make([]string, 0, 2+len(fileDefs)+len(stmts))
	if false {
		// debug
		allStmts = append(allStmts, `__xgo_reg_func_old:=__xgo_reg_func; __xgo_reg_func = func(info interface{}){
			fmt.Print("reg:"+`+XgoLocalPkgName+`+"\n")
			v := reflect.ValueOf(info)
			if v.Kind() != reflect.Struct {
				panic("non struct:"+`+XgoLocalPkgName+`)
			}
			__xgo_reg_func_old(info)
		}`)
	}
	// debug, do not include file paths
	allStmts = append(allStmts, fileDefs...)
	if false {
		// debug
		pkgPath := xgo_ctxt.GetPkgPath()
		if strings.HasSuffix(pkgPath, "dao/impl") {
			if true {
				code := strings.Join(append(allStmts, stmts...), "\n")
				os.WriteFile("/tmp/test.go", []byte(code), 0755)
				panic("debug")
			}

			if len(stmts) > 100 {
				stmts = stmts[:100]
			}
		}
	}
	allStmts = append(allStmts, stmts...)
	return strings.Join(allStmts, "\n")
}
func generateRegFileCode(pkgName string, fnName string, body string) string {
	autoGenStmts := []string{
		"package " + pkgName,
		// "import \"reflect\"", // debug
		// "import \"fmt\"",     // debug
		// "const __XGO_SKIP_TRAP = true" + "\n" + // don't do this
		"func " + fnName + "(){",
		body,
		"}",
		"",
	}
	return strings.Join(autoGenStmts, "\n")
}

func generateRegHelperCode(pkgName string) string {
	code := helperCode
	if false {
		// debug
		code = strings.ReplaceAll(code, "__PKG__", xgo_ctxt.GetPkgPath())
	}
	autoGenStmts := []string{
		// padding for debug
		"// padding",
		"",
		"",
		"package " + pkgName,
		code,
	}
	return strings.Join(autoGenStmts, "\n")
}

func getFieldNames(fields []*syntax.Field) []string {
	names := make([]string, 0, len(fields))
	for _, field := range fields {
		names = append(names, getFieldName(field))
	}
	return names
}
func getFieldName(f *syntax.Field) string {
	if f == nil || f.Name == nil {
		return ""
	}
	return f.Name.Value
}

func quoteNamesExpr(names []string) string {
	if len(names) == 0 {
		return "nil"
	}
	qNames := make([]string, 0, len(names))
	for _, name := range names {
		qNames = append(qNames, strconv.Quote(name))
	}
	return "[]string{" + strings.Join(qNames, ",") + "}"
}

func isName(expr syntax.Expr, name string) bool {
	nameExp, ok := expr.(*syntax.Name)
	if !ok {
		return false
	}
	return nameExp.Value == name
}
func hasQualifiedName(expr syntax.Expr, pkg string, name string) bool {
	sel, ok := expr.(*syntax.SelectorExpr)
	if !ok {
		return false
	}
	if sel.Sel.Value != name {
		return false
	}
	return isName(sel.X, pkg)
}
