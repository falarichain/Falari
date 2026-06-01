// Package wasm provides a wazero-based WASM runtime for executing smart contracts
// on the Falari chain. It handles compilation, instantiation, context-based gas
// metering, and deterministic execution enforcement.
package wasm

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

// DefaultGasLimit is the default gas limit per contract call.
const DefaultGasLimit uint64 = 10_000_000

// MaxBytecodeSize is the maximum allowed WASM module size in bytes.
const MaxBytecodeSize = 2 * 1024 * 1024 // 2 MB

// executionTimeout is the wall-clock deadline for a single contract call.
const executionTimeout = 30 * time.Second

// HostFunctionRegistrar is a callback that registers host functions on a
// wazero HostModuleBuilder. The chain package uses this to bind its Host API.
type HostFunctionRegistrar func(builder wazero.HostModuleBuilder)

// GasMeter tracks gas consumption during a contract call. Host functions
// call Consume() to charge gas; if the limit is exceeded it returns an error.
type GasMeter struct {
	Limit uint64
	Used  uint64
}

// Consume charges the given amount of gas. Returns an error if the limit
// would be exceeded.
func (g *GasMeter) Consume(amount uint64) error {
	if g.Used+amount < g.Used {
		return errors.New("gas overflow")
	}
	if g.Used+amount > g.Limit {
		return fmt.Errorf("out of gas: used %d + %d > limit %d", g.Used, amount, g.Limit)
	}
	g.Used += amount
	return nil
}

// Remaining returns the unused gas.
func (g *GasMeter) Remaining() uint64 {
	if g.Used >= g.Limit {
		return 0
	}
	return g.Limit - g.Used
}

// context key for gas meter (unexported to avoid collisions).
type gasMeterKey struct{}

// GasMeterFromContext extracts the GasMeter from the context.
func GasMeterFromContext(ctx context.Context) *GasMeter {
	if gm, ok := ctx.Value(gasMeterKey{}).(*GasMeter); ok {
		return gm
	}
	return nil
}

// WithGasMeter returns a new context carrying the given GasMeter.
func WithGasMeter(ctx context.Context, gm *GasMeter) context.Context {
	return context.WithValue(ctx, gasMeterKey{}, gm)
}

// CallResult holds the output of a WASM contract call.
type CallResult struct {
	Data    []byte // raw result bytes from the contract
	GasUsed uint64 // gas consumed
}

// WasmEngine manages the wazero runtime, compiled module cache, and contract
// execution lifecycle.
type WasmEngine struct {
	mu            sync.Mutex
	runtime       wazero.Runtime
	compiledCache map[string]wazero.CompiledModule // codeHash → compiled module
}

// NewWasmEngine creates a new WASM engine with the given host function registrar.
func NewWasmEngine(registrar HostFunctionRegistrar) (*WasmEngine, error) {
	ctx := context.Background()

	cfg := wazero.NewRuntimeConfig().
		WithCloseOnContextDone(true)

	rt := wazero.NewRuntimeWithConfig(ctx, cfg)

	// Register the host "env" module with all host API functions.
	envBuilder := rt.NewHostModuleBuilder("env")
	if registrar != nil {
		registrar(envBuilder)
	}
	if _, err := envBuilder.Instantiate(ctx); err != nil {
		rt.Close(ctx)
		return nil, fmt.Errorf("failed to instantiate env module: %w", err)
	}

	// Register WASI preview1 (needed by some Rust-compiled WASM modules for
	// basic syscalls like proc_exit).
	if _, err := wasi_snapshot_preview1.Instantiate(ctx, rt); err != nil {
		rt.Close(ctx)
		return nil, fmt.Errorf("failed to instantiate WASI: %w", err)
	}

	return &WasmEngine{
		runtime:       rt,
		compiledCache: make(map[string]wazero.CompiledModule),
	}, nil
}

// Close shuts down the runtime and releases all resources.
func (e *WasmEngine) Close(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	for k := range e.compiledCache {
		delete(e.compiledCache, k)
	}
	return e.runtime.Close(ctx)
}

// ComputeCodeHash returns the SHA256 hex digest of WASM bytecode.
func ComputeCodeHash(bytecode []byte) string {
	h := sha256.Sum256(bytecode)
	return hex.EncodeToString(h[:])
}

// CompileModule compiles WASM bytecode and caches it by its code hash.
func (e *WasmEngine) CompileModule(ctx context.Context, bytecode []byte) (string, error) {
	if len(bytecode) > MaxBytecodeSize {
		return "", fmt.Errorf("WASM bytecode size %d exceeds limit %d", len(bytecode), MaxBytecodeSize)
	}

	codeHash := ComputeCodeHash(bytecode)

	e.mu.Lock()
	if _, ok := e.compiledCache[codeHash]; ok {
		e.mu.Unlock()
		return codeHash, nil
	}
	e.mu.Unlock()

	compiled, err := e.runtime.CompileModule(ctx, bytecode)
	if err != nil {
		return "", fmt.Errorf("WASM compilation failed: %w", err)
	}

	e.mu.Lock()
	e.compiledCache[codeHash] = compiled
	e.mu.Unlock()

	return codeHash, nil
}

// HasModule returns true if a module with the given code hash is cached.
func (e *WasmEngine) HasModule(codeHash string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	_, ok := e.compiledCache[codeHash]
	return ok
}

// RecompileAll recompiles all provided bytecode modules into the cache.
// This is used after node restart to restore the compiled module cache
// from persisted bytecode. Modules that fail to compile are skipped with
// a warning logged; compilation errors are non-fatal.
func (e *WasmEngine) RecompileAll(ctx context.Context, modules map[string][]byte) (compiled int, errs []error) {
	for codeHash, bytecode := range modules {
		e.mu.Lock()
		_, alreadyCached := e.compiledCache[codeHash]
		e.mu.Unlock()
		if alreadyCached {
			continue
		}
		if len(bytecode) > MaxBytecodeSize {
			errs = append(errs, fmt.Errorf("module %s: bytecode too large", codeHash))
			continue
		}
		compiled_, err := e.runtime.CompileModule(ctx, bytecode)
		if err != nil {
			errs = append(errs, fmt.Errorf("module %s: %w", codeHash, err))
			continue
		}
		e.mu.Lock()
		e.compiledCache[codeHash] = compiled_
		e.mu.Unlock()
		compiled++
	}
	return compiled, errs
}

// CallExport instantiates a compiled module, calls the specified export with
// the given JSON input, and returns the result. Gas is tracked via a GasMeter
// in the context, consumed by host functions.
//
// Calling convention:
//   - Contract exports "alloc" (size i32) -> i32 for memory allocation
//   - Contract target method signature: (ptr i32, len i32) -> (packed i64)
//     where packed i64 = (result_ptr << 32) | result_len
func (e *WasmEngine) CallExport(
	ctx context.Context,
	codeHash string,
	method string,
	input []byte,
	gasLimit uint64,
) (*CallResult, error) {
	e.mu.Lock()
	compiled, ok := e.compiledCache[codeHash]
	e.mu.Unlock()
	if !ok {
		return nil, errors.New("WASM module not found in cache: " + codeHash)
	}

	if gasLimit == 0 {
		gasLimit = DefaultGasLimit
	}

	// Create gas meter and inject into context.
	gm := &GasMeter{Limit: gasLimit}
	execCtx := WithGasMeter(ctx, gm)

	// Add timeout.
	execCtx, cancel := context.WithTimeout(execCtx, executionTimeout)
	defer cancel()

	// Instantiate the module (anonymous to avoid name conflicts).
	modCfg := wazero.NewModuleConfig().
		WithName("").
		WithStartFunctions()

	mod, err := e.runtime.InstantiateModule(execCtx, compiled, modCfg)
	if err != nil {
		return nil, fmt.Errorf("WASM instantiation failed: %w", err)
	}
	defer mod.Close(execCtx)

	// Write input data into WASM linear memory via the contract's "alloc".
	inputPtr, inputLen, err := writeToMemory(execCtx, mod, input)
	if err != nil {
		return &CallResult{GasUsed: gm.Used}, fmt.Errorf("failed to write input to memory: %w", err)
	}

	// Look up the target export.
	fn := mod.ExportedFunction(method)
	if fn == nil {
		return &CallResult{GasUsed: gm.Used}, fmt.Errorf("export %q not found in WASM module", method)
	}

	// Call the function: (ptr, len) -> packed_i64
	results, err := fn.Call(execCtx, uint64(inputPtr), uint64(inputLen))
	if err != nil {
		return &CallResult{GasUsed: gm.Used}, fmt.Errorf("WASM execution error in %q: %w", method, err)
	}

	// Read result from memory.
	var resultData []byte
	if len(results) >= 1 && results[0] != 0 {
		resultPtr, resultLen := UnpackPtrLen(results[0])
		if resultLen > 0 {
			resultData, err = readFromMemory(mod, resultPtr, resultLen)
			if err != nil {
				return &CallResult{GasUsed: gm.Used}, fmt.Errorf("failed to read result from memory: %w", err)
			}
		}
	}

	return &CallResult{
		Data:    resultData,
		GasUsed: gm.Used,
	}, nil
}

// writeToMemory allocates space in the WASM module's linear memory using the
// contract's "alloc" export, then writes the data.
func writeToMemory(ctx context.Context, mod api.Module, data []byte) (uint32, uint32, error) {
	if len(data) == 0 {
		return 0, 0, nil
	}

	allocFn := mod.ExportedFunction("alloc")
	if allocFn == nil {
		return 0, 0, errors.New("contract missing required export: alloc")
	}

	size := uint32(len(data))
	results, err := allocFn.Call(ctx, uint64(size))
	if err != nil {
		return 0, 0, fmt.Errorf("alloc(%d) failed: %w", size, err)
	}
	if len(results) == 0 {
		return 0, 0, errors.New("alloc returned no value")
	}

	ptr := uint32(results[0])
	if ptr == 0 {
		return 0, 0, errors.New("alloc returned null pointer")
	}

	mem := mod.Memory()
	if !mem.Write(ptr, data) {
		return 0, 0, fmt.Errorf("memory write failed at ptr=%d len=%d", ptr, size)
	}

	return ptr, size, nil
}

// readFromMemory reads data from the WASM module's linear memory.
func readFromMemory(mod api.Module, ptr, length uint32) ([]byte, error) {
	if length == 0 {
		return nil, nil
	}

	mem := mod.Memory()
	data, ok := mem.Read(ptr, length)
	if !ok {
		return nil, fmt.Errorf("memory read failed at ptr=%d len=%d", ptr, length)
	}

	result := make([]byte, len(data))
	copy(result, data)
	return result, nil
}

// WriteToMemory is exported for host functions that need to write data to
// the contract's memory.
func WriteToMemory(ctx context.Context, mod api.Module, data []byte) (uint32, uint32, error) {
	return writeToMemory(ctx, mod, data)
}

// ReadFromMemory is exported for host functions that need to read data from
// the contract's memory.
func ReadFromMemory(mod api.Module, ptr, length uint32) ([]byte, error) {
	return readFromMemory(mod, ptr, length)
}

// PackPtrLen packs a pointer and length into a single uint64 for the contract
// calling convention: high 32 bits = pointer, low 32 bits = length.
func PackPtrLen(ptr uint32, length uint32) uint64 {
	return uint64(ptr)<<32 | uint64(length)
}

// UnpackPtrLen unpacks a packed uint64 into pointer and length.
func UnpackPtrLen(packed uint64) (uint32, uint32) {
	return uint32(packed >> 32), uint32(packed)
}
