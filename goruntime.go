/*
The MIT License (MIT)

Copyright (c) 2021 Yasuhiro Matsumoto

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
*/

package wasm

import (
	"encoding/binary"
	"errors"
	"fmt"
	"log"
	"math"
	"os"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/dop251/goja"
	"github.com/wasmerio/wasmer-go/wasmer"
)

// GoInstance is instance of Go Runtime.
type GoInstance struct {
	vm   *goja.Runtime
	this *goja.Object

	inst   *wasmer.Instance
	mem    *wasmer.Memory
	getsp  wasmer.NativeFunction
	resume wasmer.NativeFunction
	values map[uint32]goja.Value
	ids    map[goja.Value]uint32
}

// Get return Go value specified by name
func (d *GoInstance) Get(name string) goja.Value {
	return d.values[5].(*goja.Object).Get(name)
}

func (d *GoInstance) getInt32(addr int32) int32 {
	return int32(binary.LittleEndian.Uint32(d.mem.Data()[addr+0:]))
}

func (d *GoInstance) getInt64(addr int32) int64 {
	low := binary.LittleEndian.Uint32(d.mem.Data()[addr+0:])
	high := binary.LittleEndian.Uint32(d.mem.Data()[addr+4:])
	return int64(low) + int64(high)*4294967296
}

func (d *GoInstance) setInt32(addr int32, v int64) {
	binary.LittleEndian.PutUint32(d.mem.Data()[addr+0:], uint32(v))
}

func (d *GoInstance) setInt64(addr int32, v int64) {
	binary.LittleEndian.PutUint32(d.mem.Data()[addr+0:], uint32(v))
	binary.LittleEndian.PutUint32(d.mem.Data()[addr+4:], uint32(v/4294967296))
}

func (d *GoInstance) setUint8(addr int32, v uint8) {
	d.mem.Data()[addr] = v
}

func (d *GoInstance) reflectSet(v *goja.Object, key string, value goja.Value) {
	if v == nil {
		panic(d.values[5])
	}
	v.Set(key, value)
}

func (d *GoInstance) reflectDelete(v *goja.Object, key string) {
	if v == nil {
		v = d.values[5].(*goja.Object)
	}
	v.Delete(key)
}

func (d *GoInstance) reflectGet(v *goja.Object, key string) goja.Value {
	if v == nil {
		v = d.values[5].(*goja.Object)
	}
	return v.Get(key)
}

func (d *GoInstance) loadString(addr int32) string {
	array := d.getInt64(addr + 0)
	alen := d.getInt64(addr + 8)
	return string(d.mem.Data()[array : array+alen])
}

func (d *GoInstance) loadSlice(addr int32) []byte {
	array := d.getInt64(addr + 0)
	alen := d.getInt64(addr + 8)
	return d.mem.Data()[array : array+alen]
}

func (d *GoInstance) loadValue(addr int32) goja.Value {
	bits := binary.LittleEndian.Uint64(d.mem.Data()[addr+0:])
	fv := math.Float64frombits(bits)
	if fv == 0 {
		return goja.Undefined()
	}
	if !math.IsNaN(fv) {
		return d.vm.ToValue(fv)
	}
	id := binary.LittleEndian.Uint32(d.mem.Data()[addr+0:])
	//fmt.Println("loadValue", id, data.values[id])
	return d.values[id]
}

func (d *GoInstance) loadSliceOfValues(addr int32) []goja.Value {
	array := d.getInt64(addr + 0)
	alen := d.getInt64(addr + 8)
	results := []goja.Value{}
	for i := int64(0); i < alen; i++ {
		results = append(results, d.loadValue(int32(array+i*8)))
	}
	return results
}

func (d *GoInstance) storeValue(addr int32, v goja.Value) {
	nanHead := 0x7FF80000

	switch val := v.Export().(type) {
	case int64:
		v = d.vm.ToValue(float64(val))
	}
	//fmt.Printf("storeValue %v %v\n", addr, v)
	switch t := v.Export().(type) {
	case float64:
		if t != 0 {
			if math.IsNaN(t) {
				binary.LittleEndian.PutUint32(d.mem.Data()[addr+4:], uint32(nanHead))
				binary.LittleEndian.PutUint32(d.mem.Data()[addr+0:], 0)
				return
			}
			bits := math.Float64bits(t)
			binary.LittleEndian.PutUint64(d.mem.Data()[addr+0:], bits)
			return
		}
	case nil:
		bits := math.Float64bits(0)
		binary.LittleEndian.PutUint64(d.mem.Data()[addr+0:], bits)
		return
	default:
	}

	id, ok := d.ids[v]
	if !ok {
		id = uint32(len(d.values))
		d.ids[v] = id
	}
	d.values[id] = v

	typeFlag := 0
	_, ok = goja.AssertFunction(v)
	if !ok {
		if _, ok := v.(*goja.Object); ok {
			typeFlag = 1
		} else {
			switch v.Export().(type) {
			case string:
				typeFlag = 2
			}
		}

	} else {
		typeFlag = 4
	}

	binary.LittleEndian.PutUint32(d.mem.Data()[addr+4:], uint32(nanHead|typeFlag))
	binary.LittleEndian.PutUint32(d.mem.Data()[addr:], id)
}

var preCompiledInstanceOf = goja.MustCompile("", `
function(a,b){
	return a instanceof b
}
`, false)

func goRuntime(store *wasmer.Store, data *GoInstance) map[string]wasmer.IntoExtern {
	data.this = data.vm.ToValue(map[string]interface{}{
		"_pendingEvent": map[string]interface{}{
			"id":   0,
			"this": nil,
		},
		"_makeFuncWrapper": data.vm.ToValue(func(args goja.FunctionCall) goja.Value {
			id := args.Arguments[0]
			return data.vm.ToValue(func(args goja.FunctionCall) goja.Value {
				event := data.vm.ToValue(map[string]interface{}{
					"id":   id,
					"this": nil,
					"args": args,
				})
				data.values[6].ToObject(data.vm).Set("_pendingEvent", event)
				_, err := data.resume()
				if err != nil {
					log.Print("err", err)
				}
				return event.(*goja.Object).Get("result")
			})
		}),
	}).(*goja.Object)
	data.values = map[uint32]goja.Value{
		0: goja.NaN(),
		1: data.vm.ToValue(0),
		2: goja.Null(),
		3: data.vm.ToValue(true),
		4: data.vm.ToValue(false),
		5: data.vm.GlobalObject(),
		6: data.this,
	}
	data.ids = map[goja.Value]uint32{
		data.vm.ToValue(0):     1,
		goja.Null():            2,
		data.vm.ToValue(true):  3,
		data.vm.ToValue(false): 4,
		data.vm.GlobalObject(): 5,
		data.this:              6,
	}

	return map[string]wasmer.IntoExtern{
		"debug": wasmer.NewFunction(
			store,
			wasmer.NewFunctionType(wasmer.NewValueTypes(wasmer.I32), wasmer.NewValueTypes()),
			func(args []wasmer.Value) ([]wasmer.Value, error) {
				sp := args[0].Unwrap()
				fmt.Println("DEBUG", sp)
				return []wasmer.Value{}, nil
			},
		),
		"runtime.resetMemoryDataView": wasmer.NewFunction(
			store,
			wasmer.NewFunctionType(wasmer.NewValueTypes(wasmer.I32), wasmer.NewValueTypes()),
			func(args []wasmer.Value) ([]wasmer.Value, error) {
				//println("runtime.resetMemoryDataView")
				return []wasmer.Value{}, nil
			},
		),
		"runtime.wasmExit": wasmer.NewFunction(
			store,
			wasmer.NewFunctionType(wasmer.NewValueTypes(wasmer.I32), wasmer.NewValueTypes()),
			func(args []wasmer.Value) ([]wasmer.Value, error) {
				//println("runtime.wasmExit")
				sp := args[0].I32()
				sp >>= 0
				os.Exit(int(data.getInt32(sp + 8)))
				return []wasmer.Value{}, nil
			},
		),
		"runtime.wasmWrite": wasmer.NewFunction(
			store,
			wasmer.NewFunctionType(wasmer.NewValueTypes(wasmer.I32), wasmer.NewValueTypes()),
			func(args []wasmer.Value) ([]wasmer.Value, error) {
				sp := args[0].I32()
				sp >>= 0
				fd := data.getInt64(sp + 8)
				p := data.getInt64(sp + 16)
				n := data.getInt32(sp + 24)
				switch fd {
				case 1:
					os.Stdout.Write(data.mem.Data()[p : p+int64(n)])
				case 2:
					os.Stderr.Write(data.mem.Data()[p : p+int64(n)])
				}
				return []wasmer.Value{}, nil
			},
		),
		"runtime.nanotime1": wasmer.NewFunction(
			store,
			wasmer.NewFunctionType(wasmer.NewValueTypes(wasmer.I32), wasmer.NewValueTypes()),
			func(args []wasmer.Value) ([]wasmer.Value, error) {
				//println("runtime.nanotime1")
				sp := args[0].I32()
				sp >>= 0
				data.setInt64(sp+8, time.Now().UnixNano())
				return []wasmer.Value{}, nil
			},
		),
		"runtime.walltime": wasmer.NewFunction(
			store,
			wasmer.NewFunctionType(wasmer.NewValueTypes(wasmer.I32), wasmer.NewValueTypes()),
			func(args []wasmer.Value) ([]wasmer.Value, error) {
				//println("runtime.walltime")
				sp := args[0].I32()
				sp >>= 0
				msec := time.Now().UnixNano() / int64(time.Millisecond)
				data.setInt64(sp+8, msec/1000)
				data.setInt32(sp+16, (msec%1000)*1000000)
				return []wasmer.Value{}, nil
			},
		),
		"runtime.scheduleTimeoutEvent": wasmer.NewFunction(
			store,
			wasmer.NewFunctionType(wasmer.NewValueTypes(wasmer.I32), wasmer.NewValueTypes()),
			func(args []wasmer.Value) ([]wasmer.Value, error) {
				//println("runtime.scheduleTimeoutEvent")
				return []wasmer.Value{}, nil
			},
		),
		"runtime.clearTimeoutEvent": wasmer.NewFunction(
			store,
			wasmer.NewFunctionType(wasmer.NewValueTypes(wasmer.I32), wasmer.NewValueTypes()),
			func(args []wasmer.Value) ([]wasmer.Value, error) {
				//println("runtime.clearTimeoutEvent")
				return []wasmer.Value{}, nil
			},
		),
		"runtime.getRandomData": wasmer.NewFunction(
			store,
			wasmer.NewFunctionType(wasmer.NewValueTypes(wasmer.I32), wasmer.NewValueTypes()),
			func(args []wasmer.Value) ([]wasmer.Value, error) {
				//println("runtime.getRandomData")
				sp := args[0].I32()
				sp >>= 0
				data.loadSlice(sp + 8)
				return []wasmer.Value{}, nil
			},
		),
		"syscall/js.finalizeRef": wasmer.NewFunction(
			store,
			wasmer.NewFunctionType(wasmer.NewValueTypes(wasmer.I32), wasmer.NewValueTypes()),
			func(args []wasmer.Value) ([]wasmer.Value, error) {
				//println("syscall/js.finalizeRef")
				return []wasmer.Value{}, nil
			},
		),
		"syscall/js.stringVal": wasmer.NewFunction(
			store,
			wasmer.NewFunctionType(wasmer.NewValueTypes(wasmer.I32), wasmer.NewValueTypes()),
			func(args []wasmer.Value) ([]wasmer.Value, error) {
				//println("syscall/js.stringVal")
				sp := args[0].I32()
				sp >>= 0
				data.storeValue(sp+24, data.vm.ToValue(data.loadString(sp+8)))
				return []wasmer.Value{}, nil
			},
		),
		"syscall/js.valueGet": wasmer.NewFunction(
			store,
			wasmer.NewFunctionType(wasmer.NewValueTypes(wasmer.I32), wasmer.NewValueTypes()),
			func(args []wasmer.Value) (vals []wasmer.Value, err error) {
				//println("syscall/js.valueGet")
				defer recover()
				vals = []wasmer.Value{}
				err = errors.New("TypeError: Cannot convert undefined or null to object")

				sp := args[0].I32()
				sp >>= 0
				o, ok := data.loadValue(sp + 8).(*goja.Object)
				if !ok {
					return
				}
				result := data.reflectGet(o, data.loadString(sp+16))
				if v, err := data.getsp(); err == nil {
					sp = v.(int32)
					sp >>= 0
					data.storeValue(sp+32, result)
				}
				return vals, nil
			},
		),
		"syscall/js.valueSet": wasmer.NewFunction(
			store,
			wasmer.NewFunctionType(wasmer.NewValueTypes(wasmer.I32), wasmer.NewValueTypes()),
			func(args []wasmer.Value) (vals []wasmer.Value, err error) {
				//println("syscall/js.valueSet")
				defer recover()
				vals = []wasmer.Value{}
				err = errors.New("TypeError: Cannot convert undefined or null to object")

				sp := args[0].I32()
				sp >>= 0
				o, ok := data.loadValue(sp + 8).(*goja.Object)
				if !ok {
					return
				}
				data.reflectSet(o, data.loadString(sp+16), data.loadValue(sp+32))
				return vals, nil
			},
		),
		"syscall/js.valueDelete": wasmer.NewFunction(
			store,
			wasmer.NewFunctionType(wasmer.NewValueTypes(wasmer.I32), wasmer.NewValueTypes()),
			func(args []wasmer.Value) (vals []wasmer.Value, err error) {
				//println("syscall/js.valueDelete")

				defer recover()
				vals = []wasmer.Value{}
				err = errors.New("TypeError: Cannot convert undefined or null to object")

				sp := args[0].I32()
				sp >>= 0

				o, ok := data.loadValue(sp + 8).(*goja.Object)
				if !ok {
					return
				}
				data.reflectDelete(o, data.loadString(sp+16))
				return vals, nil
			},
		),
		"syscall/js.valueIndex": wasmer.NewFunction(
			store,
			wasmer.NewFunctionType(wasmer.NewValueTypes(wasmer.I32), wasmer.NewValueTypes()),
			func(args []wasmer.Value) (vals []wasmer.Value, err error) {
				//println("syscall/js.valueIndex")

				defer recover()
				vals = []wasmer.Value{}
				err = errors.New("TypeError: Cannot convert undefined or null to object")

				sp := args[0].I32()
				sp >>= 0
				o, ok := data.loadValue(sp + 8).(*goja.Object)
				if !ok {
					return
				}

				data.storeValue(sp+24, data.reflectGet(o, strconv.FormatInt(data.getInt64(sp+16), 10)))
				return []wasmer.Value{}, nil
			},
		),
		"syscall/js.valueSetIndex": wasmer.NewFunction(
			store,
			wasmer.NewFunctionType(wasmer.NewValueTypes(wasmer.I32), wasmer.NewValueTypes()),
			func(args []wasmer.Value) (vals []wasmer.Value, err error) {
				//println("syscall/js.valueSetIndex")

				defer recover()
				vals = []wasmer.Value{}
				err = errors.New("TypeError: Cannot convert undefined or null to object")

				sp := args[0].I32()
				sp >>= 0

				o, ok := data.loadValue(sp + 8).(*goja.Object)
				if !ok {
					return
				}

				data.reflectSet(o, strconv.FormatInt(data.getInt64(sp+16), 10), data.loadValue(sp+24))
				return []wasmer.Value{}, nil
			},
		),
		"syscall/js.valueInvoke": wasmer.NewFunction(
			store,
			wasmer.NewFunctionType(wasmer.NewValueTypes(wasmer.I32), wasmer.NewValueTypes()),
			func(args []wasmer.Value) (vals []wasmer.Value, err error) {
				//println("syscall/js.valueInvoke")
				vals = []wasmer.Value{}

				sp := args[0].I32()
				sp >>= 0
				m := data.loadValue(sp + 8)
				method, ok := goja.AssertFunction(m)
				if !ok {
					return []wasmer.Value{}, errors.New("js.valueInvoke: value must be function")
				}

				arg := data.loadSliceOfValues(sp + 16)
				result, err := method(goja.Undefined(), arg...)

				if err != nil {
					if v, err := data.getsp(); err == nil {
						sp = v.(int32)
						sp >>= 0
						data.storeValue(sp+40, data.vm.NewGoError(err))

						data.mem.Data()[sp+48] = 0
					}
				} else {
					if v, err := data.getsp(); err == nil {
						sp = v.(int32)
						sp >>= 0
						data.storeValue(sp+40, result)

						data.mem.Data()[sp+48] = 1
					}
				}

				return []wasmer.Value{}, nil
			},
		),
		"syscall/js.valueCall": wasmer.NewFunction(
			store,
			wasmer.NewFunctionType(wasmer.NewValueTypes(wasmer.I32), wasmer.NewValueTypes()),
			func(args []wasmer.Value) ([]wasmer.Value, error) {
				//println("syscall/js.valueCall")
				sp := args[0].I32()
				sp >>= 0
				v := data.loadValue(sp + 8)
				if v == goja.Undefined() {
					return nil, errors.New("cannot call on undefined")
				}
				m := v.ToObject(data.vm).Get(data.loadString(sp + 16))
				if m == goja.Undefined() {
					return nil, errors.New("cannot find method on the value")
				}

				method, ok := goja.AssertFunction(m)
				if !ok {
					return nil, errors.New("cannot call on non function value")
				}
				arg := data.loadSliceOfValues(sp + 32)

				result, err := method(v, arg...)

				if err != nil {
					if v, err := data.getsp(); err == nil {
						sp = v.(int32)
						sp >>= 0

						data.storeValue(sp+56, data.vm.NewGoError(err))
						data.mem.Data()[sp+64] = 0
					}
				} else {
					if v, err := data.getsp(); err == nil {
						sp = v.(int32)
						sp >>= 0

						data.storeValue(sp+56, result)
						data.mem.Data()[sp+64] = 1
					}
				}

				return []wasmer.Value{}, nil
			},
		),
		"syscall/js.valueNew": wasmer.NewFunction(
			store,
			wasmer.NewFunctionType(wasmer.NewValueTypes(wasmer.I32), wasmer.NewValueTypes()),
			func(arg []wasmer.Value) ([]wasmer.Value, error) {
				//println("syscall/js.valueNew")
				sp := arg[0].I32()
				sp >>= 0

				v := data.loadValue(sp + 8)
				args := data.loadSliceOfValues(sp + 16)
				result, err := data.vm.New(v, args...)

				s, err := data.getsp() // see comment above
				if err != nil {
					return []wasmer.Value{}, err
				}
				sp = s.(int32)
				sp >>= 0
				if err != nil {
					data.storeValue(sp+40, result)
					data.mem.Data()[sp+48] = 1
				} else {
					data.storeValue(sp+40, data.vm.NewGoError(err))
					data.mem.Data()[sp+48] = 0
				}

				return []wasmer.Value{}, nil
			},
		),
		"syscall/js.valueLength": wasmer.NewFunction(
			store,
			wasmer.NewFunctionType(wasmer.NewValueTypes(wasmer.I32), wasmer.NewValueTypes()),
			func(args []wasmer.Value) ([]wasmer.Value, error) {
				//println("syscall/js.valueLength")
				sp := args[0].I32()
				sp >>= 0
				val := data.loadValue(sp + 8)
				if val == goja.Undefined() || val == goja.NaN() || val == goja.Null() {
					return nil, errors.New("Cannot read property 'length' of undefined")
				}
				obj := val.ToObject(data.vm)
				l := obj.Get("length")
				switch v := l.Export().(type) {
				case int64:
					data.setInt64(sp+16, v)
				case float64:
					data.setInt64(sp+16, int64(v))
				}

				return []wasmer.Value{}, nil
			},
		),
		"syscall/js.valuePrepareString": wasmer.NewFunction(
			store,
			wasmer.NewFunctionType(wasmer.NewValueTypes(wasmer.I32), wasmer.NewValueTypes()),
			func(args []wasmer.Value) ([]wasmer.Value, error) {
				//println("syscall/js.valuePrepareString")
				sp := args[0].I32()
				sp >>= 0
				val := data.loadValue(sp + 8)
				var str = val.ToString().String()
				utf8.ValidString(str)

				o, _ := data.vm.New(data.vm.Get("Uint8Array"), data.vm.ToValue(data.vm.NewArrayBuffer([]byte(str))))

				data.storeValue(sp+16, o)
				data.setInt64(sp+24, int64(len(str)))
				return []wasmer.Value{}, nil
			},
		),
		"syscall/js.valueLoadString": wasmer.NewFunction(
			store,
			wasmer.NewFunctionType(wasmer.NewValueTypes(wasmer.I32), wasmer.NewValueTypes()),
			func(args []wasmer.Value) ([]wasmer.Value, error) {
				//println("syscall/js.valueLoadString")
				sp := args[0].I32()
				sp >>= 0
				str := data.loadValue(sp + 8)
				if str == goja.Undefined() || str == goja.NaN() || str == goja.Null() {
					return []wasmer.Value{}, nil
				}
				b := str.ToObject(data.vm)
				var buf []byte
				ar, ok := b.Export().(goja.ArrayBuffer)
				if !ok {
					if b, ok := str.Export().(goja.ArrayBuffer); ok {
						buf = b.Bytes()
					}
				}
				buf = ar.Bytes()
				if buf != nil {
					copy(data.loadSlice(sp+16), buf)
				}
				return []wasmer.Value{}, nil
			},
		),
		"syscall/js.valueInstanceOf": wasmer.NewFunction(
			store,
			wasmer.NewFunctionType(wasmer.NewValueTypes(wasmer.I32), wasmer.NewValueTypes()),
			func(args []wasmer.Value) ([]wasmer.Value, error) {
				//println("syscall/js.valueInstanceOf")
				sp := args[0].I32()
				sp >>= 0
				v1 := data.loadValue(sp + 8)
				v2 := data.loadValue(sp + 16)
				re, _ := data.vm.RunProgram(preCompiledInstanceOf)
				c, _ := goja.AssertFunction(re)
				isInstanceof, _ := c(goja.Null(), v1, v2)

				if isInstanceof.ToBoolean() {
					data.mem.Data()[sp+24] = 1
				} else {
					data.mem.Data()[sp+24] = 0
				}
				return []wasmer.Value{}, nil
			},
		),
		"syscall/js.copyBytesToGo": wasmer.NewFunction(
			store,
			wasmer.NewFunctionType(wasmer.NewValueTypes(wasmer.I32), wasmer.NewValueTypes()),
			func(args []wasmer.Value) ([]wasmer.Value, error) {
				//println("syscall/js.copyBytesToJS")
				sp := args[0].I32()
				sp >>= 0
				dst := data.loadSlice(sp + 8)
				src := data.loadValue(sp + 32)

				obj, ok := src.(*goja.Object)
				if !ok {
					data.setUint8(sp+48, 0)
					return []wasmer.Value{}, nil
				}
				ar := obj.Get("buffer").Export()
				arr, ok := ar.(goja.ArrayBuffer)
				if !ok {
					data.setUint8(sp+48, 0)
					return []wasmer.Value{}, nil
				}

				copy(dst, arr.Bytes()[:len(dst)])
				data.setInt64(sp+40, int64(len(dst)))
				data.setUint8(sp+48, 1)

				return []wasmer.Value{}, nil
			},
		),
		"syscall/js.copyBytesToJS": wasmer.NewFunction(
			store,
			wasmer.NewFunctionType(wasmer.NewValueTypes(wasmer.I32), wasmer.NewValueTypes()),
			func(args []wasmer.Value) ([]wasmer.Value, error) {
				//println("syscall/js.copyBytesToJS")
				sp := args[0].I32()
				sp >>= 0
				dst := data.loadValue(sp + 8)
				src := data.loadSlice(sp + 32)

				obj, ok := dst.(*goja.Object)
				if !ok {
					data.setUint8(sp+48, 0)
					return []wasmer.Value{}, nil
				}
				ar := obj.Get("buffer").Export()
				arr, ok := ar.(goja.ArrayBuffer)
				if !ok {
					data.setUint8(sp+48, 0)
					return []wasmer.Value{}, nil
				}

				d := arr.Bytes()
				copy(d, src[:len(d)])
				data.setInt64(sp+40, int64(len(d)))
				data.setUint8(sp+48, 1)

				return []wasmer.Value{}, nil
			},
		),
	}
}
