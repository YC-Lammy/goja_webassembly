# goja_webassembly

This module provides WebAssembly functions into goja javascript engine.

example:
```go
package main

import(
    "github.com/dop251/goja"
    "github.com/YC-Lammy/goja_webassembly"
)

func main(){
    vm := goja.New()
    webassembly.Enable(vm)
    
    wasmbytes, _ := wasmer.Wat2Wasm(`
    (module
  (type $sum_t (func (param i32 i32) (result i32)))
  (func $sum_f (type $sum_t) (param $x i32) (param $y i32) (result i32)
    local.get $x
    local.get $y
    i32.add)
    (export "sum" (func $sum_f)))`)
    
    vm.GlobalObject().Set("wasmbytes",vm.ToValue(vm.NewArrayBuffer(wasmbytes)))
  
    vm.RunString(`
        instance = WebAssembly.instantiate(wasmbytes)
        instance.exports.sum(3,4)
    `)
}
```
