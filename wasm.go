//+build cgo

package wasm

import (
	"reflect"
	"strconv"
	"strings"
	"unsafe"

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

	module := wasmObj.Get("Module").ToObject(vm)

	module.Set("exports", func(arg goja.FunctionCall) goja.Value {
		module, ok := arg.Argument(0).Export().(*WasmModule)
		if !ok {
			panic(vm.NewTypeError("WebAssembly.Module.exports(): argument 1 must be WebAssembly.Module"))
		}
		return module.Exports()
	})
	module.Set("imports", func(arg goja.FunctionCall) goja.Value {
		module, ok := arg.Argument(0).Export().(*WasmModule)
		if !ok {
			panic(vm.NewTypeError("WebAssembly.Module.imports(): argument 1 must be WebAssembly.Module"))
		}
		return module.Imports()
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
			importObject = imp.Init(store)

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

	wasmObj.Set("Global", func(c goja.ConstructorCall, vm *goja.Runtime) *goja.Object {
		glob := &WasmGlobal{
			vm:   vm,
			val:  int32(0),
			kind: wasmer.I32,
		}
		a1 := c.Argument(0).Export()
		if a1 != nil {
			if v, ok := a1.(map[string]interface{}); ok {
				if mut, ok := v["mutable"]; ok {
					switch v := mut.(type) {
					case bool:
						glob.mutable = v
					case int64:
						glob.mutable = !(v == 0)
					case float64:
						glob.mutable = !(v == 0)
					case string:
						glob.mutable = !(v == "")
					case nil:
					default:
						glob.mutable = true
					}
				}
				if kind, ok := v["value"]; ok {
					if v, ok := kind.(string); ok {
						switch v {
						case "i32":
						case "i64":
							glob.kind = wasmer.I64
							glob.val = int64(0)
						case "f32":
							glob.kind = wasmer.F32
							glob.val = float32(0)
						case "f64":
							glob.kind = wasmer.F64
							glob.val = float64(0)
						}
					}
				}
			}
			a2 := c.Argument(1).Export()
			if a2 != nil {
				if v, ok := a2.(int64); ok {
					switch glob.kind {
					case wasmer.I32:
						glob.val = int32(v)
					case wasmer.I64:
						glob.val = int64(v)
					case wasmer.F32:
						glob.val = float32(v)
					case wasmer.F64:
						glob.val = float64(v)
					}
				}
				if v, ok := a2.(float64); ok {
					switch glob.kind {
					case wasmer.I32:
						glob.val = int32(v)
					case wasmer.I64:
						glob.val = int64(v)
					case wasmer.F32:
						glob.val = float32(v)
					case wasmer.F64:
						glob.val = float64(v)
					}
				}
			}
		}

		obj := vm.NewDynamicObject(glob)
		obj.SetPrototype(c.This.Prototype())
		return obj
	})

	wasmObj.Set("Memory", func(c goja.ConstructorCall, vm *goja.Runtime) *goja.Object {
		aro := c.Argument(0).ToObject(vm)
		init := aro.Get("initial").Export()
		var initial uint32
		switch v := init.(type) {
		case int64:
			initial = uint32(v)
		case float64:
			initial = uint32(v)
		default:
			panic(vm.NewTypeError("WebAssembly.Memory(): Property 'initial' must be convertible to a valid number"))
		}
		max := wasmer.LimitMaxUnbound()
		if v := aro.Get("maximum"); v != nil {
			switch v := v.Export().(type) {
			case int64:
				max = uint32(v)
			case float64:
				max = uint32(v)
			default:
				panic(vm.NewTypeError("WebAssembly.Memory(): Property 'maximum' must be convertible to a valid number"))
			}
		}

		obj := vm.NewDynamicObject(&WasmMemory{
			vm:      vm,
			init:    initial,
			current: initial,
			max:     max,
		})
		obj.SetPrototype(c.This.Prototype())
		return obj
	})

	wasmObj.Set("Table", func(c goja.ConstructorCall, vm *goja.Runtime) *goja.Object {
		obj := vm.NewDynamicObject(&WasmTable{
			vm: vm,
		})
		obj.SetPrototype(c.This.Prototype())
		return obj
	})
}

type WasmModule struct {
	vm     *goja.Runtime
	module *wasmer.Module
	store  *wasmer.Store

	exports *goja.Object
	imports *goja.Object
}

func (w *WasmModule) Exports() *goja.Object {
	if w.exports == nil {
		w.exports = w.vm.NewArray(&WasmModuleExports{
			vm:      w.vm,
			exports: w.module.Exports(),
		})
	}
	return w.exports
}

func (w *WasmModule) Imports() *goja.Object {
	if w.imports == nil {
		w.exports = w.vm.NewArray(&WasmModuleImports{
			vm:      w.vm,
			imports: w.module.Imports(),
		})
	}
	return w.imports
}

func (w *WasmModule) Get(key string) goja.Value {
	switch key {
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

var _ goja.DynamicArray

type WasmModuleExports struct {
	vm      *goja.Runtime
	exports []*wasmer.ExportType
	cached  map[int]goja.Value
}

func (w *WasmModuleExports) Get(idx int) goja.Value {
	if len(w.exports) <= idx {
		return goja.Undefined()
	}
	if v, ok := w.cached[idx]; ok {
		return v
	}
	prop := w.exports[idx]
	obj := w.vm.NewObject()
	obj.Set("name", prop.Name())
	obj.Set("kind", strings.Replace(prop.Type().Kind().String(), "func", "function", 1))
	w.cached[idx] = obj
	return obj
}

func (w *WasmModuleExports) Len() int {
	return len(w.exports)
}

func (w *WasmModuleExports) Set(idx int, val goja.Value) bool {
	return false
}

func (w *WasmModuleExports) SetLen(int) bool {
	return false
}

type WasmModuleImports struct {
	vm      *goja.Runtime
	imports []*wasmer.ImportType
	cached  map[int]*goja.Object
}

func (w *WasmModuleImports) Get(idx int) goja.Value {
	if len(w.imports) <= idx {
		return goja.Undefined()
	}
	if w.cached == nil {
		w.cached = map[int]*goja.Object{}
	}
	if v, ok := w.cached[idx]; ok {
		return v
	}
	prop := w.imports[idx]
	obj := w.vm.NewObject()
	obj.Set("name", prop.Name())
	obj.Set("kind", strings.Replace(prop.Type().Kind().String(), "func", "function", 1))
	obj.Set("module", prop.Module())
	w.cached[idx] = obj
	return obj
}

func (w *WasmModuleImports) Len() int {
	return len(w.imports)
}

func (w *WasmModuleImports) Set(idx int, val goja.Value) bool {
	return false
}

func (w *WasmModuleImports) SetLen(int) bool {
	return false
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

	cached map[string]goja.Value
}

func (in *InstanceExports) Get(key string) goja.Value {
	val, err := in.exports.Get(key)
	if err != nil {
		return goja.Undefined()
	}

	if in.cached == nil {
		in.cached = map[string]goja.Value{}
	}
	if v, ok := in.cached[key]; ok {
		return v
	}

	fn := val.IntoFunction()
	if fn != nil {
		f := in.vm.ToValue(func(arg goja.FunctionCall, vm *goja.Runtime) goja.Value {
			fntyp := fn.Type()

			params := []interface{}{}
			for i, typ := range fntyp.Params() {
				if len(arg.Arguments) == i {
					panic(vm.NewTypeError())
				}
				switch typ.Kind() {
				case wasmer.I32:
					var p int32
					switch v := arg.Arguments[i].Export().(type) {
					case int64:
						p = int32(v)
					case float64:
						p = int32(v)
					default:
						panic(in.vm.NewTypeError("WebAssembly.FunctionCall: argument " + strconv.Itoa(i) + " must be number"))
					}
					params = append(params, p)
				case wasmer.I64:
					var p int64
					switch v := arg.Arguments[i].Export().(type) {
					case int64:
						p = v
					case float64:
						p = int64(v)
					default:
						panic(in.vm.NewTypeError("WebAssembly.FunctionCall: argument " + strconv.Itoa(i) + " must be number"))
					}
					params = append(params, p)
				case wasmer.F32:
					var p float32
					switch v := arg.Arguments[i].Export().(type) {
					case int64:
						p = float32(v)
					case float64:
						p = float32(v)
					default:
						panic(in.vm.NewTypeError("WebAssembly.FunctionCall: argument " + strconv.Itoa(i) + " must be number"))
					}
					params = append(params, p)
				case wasmer.F64:
					var p float64
					switch v := arg.Arguments[i].Export().(type) {
					case int64:
						p = float64(v)
					case float64:
						p = v
					default:
						panic(in.vm.NewTypeError("WebAssembly.FunctionCall: argument " + strconv.Itoa(i) + " must be number"))
					}
					params = append(params, p)
				}

			}
			r, err := fn.Call(params...)
			if err != nil {
				panic(vm.NewTypeError("WebAssembly.FunctionCall: " + err.Error()))
			}
			return vm.ToValue(r)
		})

		in.cached[key] = f
		return f
	}

	glob := val.IntoGlobal()
	if glob != nil {
		o, _ := in.vm.RunString("new WebAssembly.Global()")
		g := o.Export().(*WasmGlobal)
		g.glob = glob
		g.kind = glob.Type().ValueType().Kind()
		g.val, _ = glob.Get()
		return o
	}

	mem := val.IntoMemory()
	if mem != nil {
		limit := mem.Type().Limits()
		o, _ := in.vm.RunString("new WebAssembly.Memory({initial:" + strconv.Itoa(int(limit.Minimum())) + ", maximum: " + strconv.Itoa(int(limit.Maximum())) + "})")
		m := o.Export().(*WasmMemory)
		m.memory = mem
		return o
	}

	table := val.IntoTable()
	if table != nil {
		arg := in.vm.NewObject()
		o, _ := in.vm.New(in.vm.GlobalObject().Get("WebAssembly").(*goja.Object).Get("Table"), arg)
		t := o.Export().(*WasmTable)
		t.table = table
		return o
	}

	return goja.Undefined()
}

func (i *InstanceExports) Set(key string, val goja.Value) bool {
	return false
}

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

type WasmGlobal struct {
	vm      *goja.Runtime
	val     interface{}
	kind    wasmer.ValueKind
	mutable bool
	glob    *wasmer.Global
}

func (w *WasmGlobal) Get(key string) goja.Value {
	switch key {

	case "value":
		if w.glob != nil {
			v, _ := w.glob.Get()
			return w.vm.ToValue(v)
		}
		return w.vm.ToValue(w.val)

	case "valueOf":
		return w.vm.ToValue(func(goja.FunctionCall) goja.Value {
			return w.Get("value")
		})
	case "toString":
		return w.vm.ToValue(func(goja.FunctionCall) goja.Value {
			return w.vm.ToValue("WebAssembly.Global")
		})
	}
	return goja.Undefined()
}

func (w *WasmGlobal) Set(key string, val goja.Value) bool {
	switch key {

	case "value":
		v := val.Export()
		kind := w.kind
		if w.glob != nil {
			kind = w.glob.Type().ValueType().Kind()
		}
		var p interface{}
		switch kind {
		case wasmer.I32:
			switch v := v.(type) {
			case int64:
				p = int32(v)
			case float64:
				p = int32(v)
			default:
				panic(w.vm.NewTypeError("WebAssembly.Global.value: argument 1 must be number"))
			}
		case wasmer.I64:
			switch v := v.(type) {
			case int64:
				p = v
			case float64:
				p = int64(v)
			default:
				panic(w.vm.NewTypeError("WebAssembly.Global.value: argument 1 must be number"))
			}
		case wasmer.F32:
			switch v := v.(type) {
			case int64:
				p = float32(v)
			case float64:
				p = float32(v)
			default:
				panic(w.vm.NewTypeError("WebAssembly.Global.value: argument 1 must be number"))
			}
		case wasmer.F64:
			switch v := v.(type) {
			case int64:
				p = float64(v)
			case float64:
				p = float64(v)
			default:
				panic(w.vm.NewTypeError("WebAssembly.Global.value: argument 1 must be number"))
			}
		}
		if w.glob != nil {
			w.glob.Set(p, kind)
		}
		w.kind = kind
		w.val = p
	}
	return true
}

func (i *WasmGlobal) Delete(key string) bool {
	return false
}

func (i *WasmGlobal) Has(key string) bool {
	for _, k := range i.Keys() {
		if k == key {
			return true
		}
	}
	return false
}

func (i *WasmGlobal) Keys() []string {
	return []string{"value"}
}

type WasmMemory struct {
	vm     *goja.Runtime
	memory *wasmer.Memory

	init    uint32
	current uint32
	max     uint32

	grow goja.Value

	membuffer   goja.Value
	dummybuffer goja.Value
}

func (w *WasmMemory) Get(key string) goja.Value {
	switch key {
	case "buffer":
		if w.membuffer != nil {
			return w.membuffer
		}
		if w.memory != nil {
			data := w.memory.Data()
			w.membuffer = w.vm.ToValue(data)
			if w.dummybuffer != nil {
				ar := w.dummybuffer.Export().(goja.ArrayBuffer)
				b := ar.Bytes()
				dummy := (*reflect.SliceHeader)(unsafe.Pointer(&b))
				current := *(*reflect.SliceHeader)(unsafe.Pointer(&data))
				dummy.Data = current.Data
				dummy.Cap = current.Cap
				dummy.Len = current.Len
			}
			return w.membuffer
		}
		if w.dummybuffer == nil {
			w.dummybuffer = w.vm.ToValue(w.vm.NewArrayBuffer(make([]byte, w.current*64*1024)))
		}
		return w.dummybuffer
	case "grow":
		if w.grow == nil {
			w.grow = w.vm.ToValue(func(arg goja.FunctionCall, vm *goja.Runtime) goja.Value {
				s := w.current
				g := arg.Argument(0).ToInteger()
				if w.memory != nil {
					s = uint32(w.memory.Size())
					w.memory.Grow(wasmer.Pages(g))
				}
				w.current += uint32(g)
				return vm.ToValue(s)
			})
		}
		return w.grow
	}
	return goja.Undefined()
}

func (w *WasmMemory) Set(key string, val goja.Value) bool {
	return false
}

func (i *WasmMemory) Delete(key string) bool {
	return false
}

func (i *WasmMemory) Has(key string) bool {
	for _, k := range i.Keys() {
		if k == key {
			return true
		}
	}
	return false
}

func (i *WasmMemory) Keys() []string {
	return []string{"value"}
}

type WasmTable struct {
	vm    *goja.Runtime
	table *wasmer.Table
}

func (w *WasmTable) Get(key string) goja.Value {
	switch key {
	}
	return goja.Undefined()
}

func (w *WasmTable) Set(key string, val goja.Value) bool {
	return false
}

func (w *WasmTable) Delete(key string) bool {
	return false
}

func (w *WasmTable) Has(key string) bool {
	for _, k := range w.Keys() {
		if k == key {
			return true
		}
	}
	return false
}

func (w *WasmTable) Keys() []string {
	return []string{}
}
