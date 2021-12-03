package wasm

import (
	"github.com/dop251/goja"
	"github.com/wasmerio/wasmer-go/wasmer"
)

func ModuleLoader(vm *goja.Runtime, module *goja.Object) {
	exports := module.Get("exports").(*goja.Object)
}

type GoClass struct {
	importObject *GoImportObject

	run goja.Value
}

type GoImportObject struct {
	importObject *wasmer.ImportObject
}

var goImportObjectCache *GoImportObject

func NewGoImportObject() *GoImportObject {
	if goImportObjectCache == nil {

	}
	return goImportObjectCache
}

func (g *GoImportObject) Get(key string) goja.Value {
	switch key {
	}
	return goja.Undefined()
}
