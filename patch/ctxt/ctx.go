package ctxt

import (
	"cmd/compile/internal/base"
	"os"
	"strings"
)

const XgoModule = "github.com/xhd2015/xgo"
const XgoRuntimePkg = XgoModule + "/runtime"
const XgoRuntimeCorePkg = XgoModule + "/runtime/core"

var XgoMainModule = os.Getenv("XGO_MAIN_MODULE")
var XgoCompilePkgDataDir = os.Getenv("XGO_COMPILE_PKG_DATA_DIR")

const XgoLinkTrapVarForGenerated = "__xgo_link_trap_var_for_generated"

func SkipPackageTrap() bool {
	pkgPath := GetPkgPath()
	if pkgPath == "" {
		return true
	}
	if strings.HasPrefix(pkgPath, "runtime/") || strings.HasPrefix(pkgPath, "internal/") {
		return true
	}
	if base.Flag.Std {
		// skip std lib, especially skip:
		//    runtime, runtime/internal, runtime/*, reflect, unsafe, syscall, sync, sync/atomic,  internal/*
		//
		// however, there are some funcs in stdlib that we can
		// trap, for example, db connection
		// for example:
		//     errors, math, math/bits, unicode, unicode/utf8, unicode/utf16, strconv, path, sort, time, encoding/json

		// NOTE: base.Flag.Std in does not always reflect func's package path,
		// because generic instantiation happens in other package, so this
		// func may be a foreigner.

		// allow http
		if _, ok := stdWhitelist[pkgPath]; ok {
			return false
		}
		return true
	}
	if isSkippableSpecialPkg(pkgPath) {
		return true
	}

	if IsPkgXgoSkipTrap(pkgPath) {
		return true
	}
	// debug
	if strings.HasPrefix(pkgPath, "crypto/") {
		return true
	}

	// TODO: may allow customize package filter
	return false
}

func AllowPkgFuncTrap(pkgPath string, isStd bool, funcName string) bool {
	if isStd {
		return allowStdFunc(pkgPath, funcName)
	}

	return true
}

// skip all packages for xgo,except test
func IsPkgXgoSkipTrap(pkg string) bool {
	suffix, ok := cutPkgPrefix(pkg, XgoModule)
	if !ok {
		return false
	}
	if suffix == "" {
		return true
	}
	// check if the package is test or runtime/test
	_, ok = cutPkgPrefix(suffix, "test")
	if ok {
		return false
	}
	_, ok = cutPkgPrefix(suffix, "runtime/test")
	if ok {
		return false
	}
	return true
}

func cutPkgPrefix(s string, pkg string) (suffix string, ok bool) {
	if !strings.HasPrefix(s, pkg) {
		return "", false
	}
	if len(s) == len(pkg) {
		return "", true
	}
	n := len(pkg)
	if s[n] != '/' {
		return "", false
	}
	return s[n+1:], true
}
