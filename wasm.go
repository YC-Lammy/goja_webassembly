//+build cgo

package wasm

import (
	"github.com/dop251/goja"
	"github.com/wasmerio/wasmer-go/wasmer"
)

var engine *wasmer.Engine

func Enable(vm *goja.Runtime) {
	engine = wasmer.NewEngine()

	wasmObj := vm.NewObject()

	global := vm.GlobalObject()
	global.DefineDataProperty("WebAssembly", wasmObj, 1, 1, 1)

	wasmObj.Set("Module", func(c goja.ConstructorCall, vm *goja.Runtime) *goja.Object {
		module := &WasmModule{}
		obj := vm.NewDynamicObject(module)
		obj.SetPrototype(c.This.Prototype())
		return obj
	})

	wasmObj.Set("Instance", func(c goja.ConstructorCall, vm *goja.Runtime) *goja.Object {
		mod := c.Argument(0).Export()
		module, ok := mod.(*WasmModule)
		if !ok {
			panic(vm.NewTypeError("WebAssembly.Instance: argument 1 must be WebAssembly.Module"))
		}

		store := module.store

		var importObject *wasmer.ImportObject
		switch imp := c.Argument(1).Export().(type) {

		case *GoImportObject:
			importObject = imp.importObject

		case map[string]interface{}:
			importObject = wasmer.NewImportObject()

		default:
			builder := wasmer.NewWasiStateBuilder(module.module.Name())
			im, _ := builder.Finalize()
			importObject, _ = im.GenerateImportObject(store, module.module)
		}
		ins, err := wasmer.NewInstance(module.module, importObject)

		if err != nil {
			panic(vm.NewTypeError("WebAssembly.Instance: " + err.Error()))
		}

		instance := &WasmInstance{
			vm:       vm,
			instance: ins,
		}
		obj := vm.NewDynamicObject(instance)
		obj.SetPrototype(c.This.Prototype())

		instance.this = obj
		return obj
	})

}

type WasmModule struct {
	vm     *goja.Runtime
	module *wasmer.Module
	store  *wasmer.Store
}

func (w *WasmModule) Get(key string) goja.Value {
	switch key {
	case "exports":
		if w.exports == nil {
			w.exports = w.vm.NewDynamicObject(&InstanceExports{
				vm:      w.vm,
				exports: w.instance.Exports,
			})
		}
		return w.exports
	}
	return goja.Undefined()
}

func (w *WasmModule) Set(key string, val goja.Value) bool

func (w *WasmModule) Delete(key string) bool {
	return false
}

func (w *WasmModule) Has(key string) bool {
	for _, k := range w.Keys() {
		if k == key {
			return true
		}
	}
	return false
}

func (w *WasmModule) Keys() []string {
	return []string{"exports"}
}

type WasmInstance struct {
	this *goja.Object
	vm   *goja.Runtime

	store    *wasmer.Store
	instance *wasmer.Instance

	exports goja.Value
}

func (w *WasmInstance) Get(key string) goja.Value {
	switch key {
	case "exports":
		if w.exports == nil {
			w.exports = w.vm.NewDynamicObject(&InstanceExports{
				vm:      w.vm,
				exports: w.instance.Exports,
			})
		}
		return w.exports
	}
	return goja.Undefined()
}

func (w *WasmInstance) Set(key string, val goja.Value) bool

func (w *WasmInstance) Delete(key string) bool {
	return false
}

func (w *WasmInstance) Has(key string) bool {
	for _, k := range w.Keys() {
		if k == key {
			return true
		}
	}
	return false
}

func (w *WasmInstance) Keys() []string {
	return []string{"exports"}
}

var _ goja.DynamicObject

type InstanceExports struct {
	vm      *goja.Runtime
	exports *wasmer.Exports
}

func (i *InstanceExports) Get(key string) goja.Value {
	return goja.Undefined()
}

func (i *InstanceExports) Set(key string, val goja.Value) bool

func (i *InstanceExports) Delete(key string) bool {
	return false
}

func (i *InstanceExports) Has(key string) bool {
	for _, k := range i.Keys() {
		if k == key {
			return true
		}
	}
	return false
}

func (i *InstanceExports) Keys() []string {
	return []string{"exports"}
}
