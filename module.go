package wasm

import (
	"encoding/binary"

	"github.com/dop251/goja"
	"github.com/wasmerio/wasmer-go/wasmer"
)

func RequireModuleLoader(vm *goja.Runtime, module *goja.Object) {
	exports := module.Get("exports").(*goja.Object)

	exports.Set("Go", func(c goja.ConstructorCall, vm *goja.Runtime) *goja.Object {
		class := &GoClass{
			vm: vm,
			instance: &GoInstance{
				vm: vm,
			},
		}
		obj := vm.NewDynamicObject(class)
		obj.SetPrototype(c.This.Prototype())
		return obj
	})
}

type GoClass struct {
	vm *goja.Runtime

	instance *GoInstance

	importObject goja.Value

	argv goja.Value
	run  goja.Value
}

func (g *GoClass) Get(key string) goja.Value {
	switch key {
	case "importObject":
		if g.importObject == nil {
			g.importObject = g.vm.NewDynamicObject(&GoImportObject{
				vm:      g.vm,
				goclass: g,
			})
		}
		return g.importObject

	case "argv":
		if g.argv == nil {
			g.argv = g.vm.NewArray("js")
		}
		return g.argv
	case "run":
		if g.run == nil {
			g.run = g.vm.ToValue(func(arg goja.FunctionCall, vm *goja.Runtime) goja.Value {

				a1 := arg.Argument(0).Export()
				if a1 == nil {
					panic(vm.NewTypeError("Go.run: argument 1 must be WebAssembly.Instance"))
				}
				instance, ok := a1.(*WasmInstance)
				if !ok {
					panic(vm.NewTypeError("Go.run: argument 1 must be WebAssembly.Instance"))
				}

				g.instance.inst = instance.instance

				mem, err := g.instance.inst.Exports.GetMemory("mem")
				if err != nil {
					panic(vm.NewTypeError("Go.run: " + err.Error()))
				}
				g.instance.mem = mem

				offset := 4096

				strPtr := func(str string) int {
					ptr := offset
					b := append([]byte(str), 0)
					copy(g.instance.mem.Data()[offset:offset+len(b)], b)
					offset += len(b)
					if offset%8 != 0 {
						offset += 8 - (offset % 8)
					}
					return ptr
				}
				argPtrs := []int{strPtr("js"), 0, 0}

				for _, ptr := range argPtrs {
					binary.LittleEndian.PutUint32(g.instance.mem.Data()[offset+0:], uint32(ptr))
					binary.LittleEndian.PutUint32(g.instance.mem.Data()[offset+4:], 0)
					offset += 8
				}

				getsp, err := g.instance.inst.Exports.GetFunction("getsp")
				if err != nil {
					panic(vm.NewTypeError("Go.run: " + err.Error()))
				}
				g.instance.getsp = getsp

				resume, err := g.instance.inst.Exports.GetFunction("resume")
				if err != nil {
					panic(vm.NewTypeError("Go.run: " + err.Error()))
				}
				g.instance.resume = resume

				run, err := g.instance.inst.Exports.GetFunction("run")
				if err != nil {
					panic(vm.NewTypeError("Go.run: " + err.Error()))
				}

				_, err = run(1, 4104)
				if err != nil {
					panic(vm.NewTypeError("Go.run: " + err.Error()))
				}

				return goja.Undefined()
			})
		}
		return g.run
	}
	return goja.Undefined()
}

func (g *GoClass) Set(key string, val goja.Value) bool {
	return false
}

func (g *GoClass) Delete(key string) bool {
	return false
}

func (g *GoClass) Has(key string) bool {
	for _, k := range g.Keys() {
		if k == key {
			return true
		}
	}
	return false
}

func (g *GoClass) Keys() []string {
	return []string{"importObject"}
}

type GoImportObject struct {
	vm *goja.Runtime

	goclass *GoClass
}

func (g *GoImportObject) Init(store *wasmer.Store) *wasmer.ImportObject {
	importObject := wasmer.NewImportObject()
	importObject.Register("go", goRuntime(store, g.goclass.instance))
	return importObject
}

func (g *GoImportObject) Get(key string) goja.Value {
	switch key {
	}
	return goja.Undefined()
}

func (g *GoImportObject) Set(key string, val goja.Value) bool {
	return false
}

func (g *GoImportObject) Delete(key string) bool {
	return false
}

func (g *GoImportObject) Has(key string) bool {
	for _, k := range g.Keys() {
		if k == key {
			return true
		}
	}
	return false
}

func (g *GoImportObject) Keys() []string {
	return []string{}
}
