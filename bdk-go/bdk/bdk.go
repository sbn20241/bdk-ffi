package bdk

/*
#cgo CFLAGS: -I${SRCDIR}
#cgo LDFLAGS: -L${SRCDIR}/../../bdk-ffi/target/release -lbdkffi -lm -ldl
#cgo darwin LDFLAGS: -framework CoreFoundation -framework Security
#include "bdk.h"
*/
import "C"

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"runtime"
	"sync"
	"sync/atomic"
	"unsafe"
)

// This is needed, because as of go 1.24
// type RustBuffer C.RustBuffer cannot have methods,
// RustBuffer is treated as non-local type
type GoRustBuffer struct {
	inner C.RustBuffer
}

type RustBufferI interface {
	AsReader() *bytes.Reader
	Free()
	ToGoBytes() []byte
	Data() unsafe.Pointer
	Len() uint64
	Capacity() uint64
}

func RustBufferFromExternal(b RustBufferI) GoRustBuffer {
	return GoRustBuffer{
		inner: C.RustBuffer{
			capacity: C.uint64_t(b.Capacity()),
			len:      C.uint64_t(b.Len()),
			data:     (*C.uchar)(b.Data()),
		},
	}
}

func (cb GoRustBuffer) Capacity() uint64 {
	return uint64(cb.inner.capacity)
}

func (cb GoRustBuffer) Len() uint64 {
	return uint64(cb.inner.len)
}

func (cb GoRustBuffer) Data() unsafe.Pointer {
	return unsafe.Pointer(cb.inner.data)
}

func (cb GoRustBuffer) AsReader() *bytes.Reader {
	b := unsafe.Slice((*byte)(cb.inner.data), C.uint64_t(cb.inner.len))
	return bytes.NewReader(b)
}

func (cb GoRustBuffer) Free() {
	rustCall(func(status *C.RustCallStatus) bool {
		C.ffi_bdkffi_rustbuffer_free(cb.inner, status)
		return false
	})
}

func (cb GoRustBuffer) ToGoBytes() []byte {
	return C.GoBytes(unsafe.Pointer(cb.inner.data), C.int(cb.inner.len))
}

func stringToRustBuffer(str string) C.RustBuffer {
	return bytesToRustBuffer([]byte(str))
}

func bytesToRustBuffer(b []byte) C.RustBuffer {
	if len(b) == 0 {
		return C.RustBuffer{}
	}
	// We can pass the pointer along here, as it is pinned
	// for the duration of this call
	foreign := C.ForeignBytes{
		len:  C.int(len(b)),
		data: (*C.uchar)(unsafe.Pointer(&b[0])),
	}

	return rustCall(func(status *C.RustCallStatus) C.RustBuffer {
		return C.ffi_bdkffi_rustbuffer_from_bytes(foreign, status)
	})
}

type BufLifter[GoType any] interface {
	Lift(value RustBufferI) GoType
}

type BufLowerer[GoType any] interface {
	Lower(value GoType) C.RustBuffer
}

type BufReader[GoType any] interface {
	Read(reader io.Reader) GoType
}

type BufWriter[GoType any] interface {
	Write(writer io.Writer, value GoType)
}

func LowerIntoRustBuffer[GoType any](bufWriter BufWriter[GoType], value GoType) C.RustBuffer {
	// This might be not the most efficient way but it does not require knowing allocation size
	// beforehand
	var buffer bytes.Buffer
	bufWriter.Write(&buffer, value)

	bytes, err := io.ReadAll(&buffer)
	if err != nil {
		panic(fmt.Errorf("reading written data: %w", err))
	}
	return bytesToRustBuffer(bytes)
}

func LiftFromRustBuffer[GoType any](bufReader BufReader[GoType], rbuf RustBufferI) GoType {
	defer rbuf.Free()
	reader := rbuf.AsReader()
	item := bufReader.Read(reader)
	if reader.Len() > 0 {
		// TODO: Remove this
		leftover, _ := io.ReadAll(reader)
		panic(fmt.Errorf("Junk remaining in buffer after lifting: %s", string(leftover)))
	}
	return item
}

func rustCallWithError[E any, U any](converter BufReader[*E], callback func(*C.RustCallStatus) U) (U, *E) {
	var status C.RustCallStatus
	returnValue := callback(&status)
	err := checkCallStatus(converter, status)
	return returnValue, err
}

func checkCallStatus[E any](converter BufReader[*E], status C.RustCallStatus) *E {
	switch status.code {
	case 0:
		return nil
	case 1:
		return LiftFromRustBuffer(converter, GoRustBuffer{inner: status.errorBuf})
	case 2:
		// when the rust code sees a panic, it tries to construct a rustBuffer
		// with the message.  but if that code panics, then it just sends back
		// an empty buffer.
		if status.errorBuf.len > 0 {
			panic(fmt.Errorf("%s", FfiConverterStringINSTANCE.Lift(GoRustBuffer{inner: status.errorBuf})))
		} else {
			panic(fmt.Errorf("Rust panicked while handling Rust panic"))
		}
	default:
		panic(fmt.Errorf("unknown status code: %d", status.code))
	}
}

func checkCallStatusUnknown(status C.RustCallStatus) error {
	switch status.code {
	case 0:
		return nil
	case 1:
		panic(fmt.Errorf("function not returning an error returned an error"))
	case 2:
		// when the rust code sees a panic, it tries to construct a C.RustBuffer
		// with the message.  but if that code panics, then it just sends back
		// an empty buffer.
		if status.errorBuf.len > 0 {
			panic(fmt.Errorf("%s", FfiConverterStringINSTANCE.Lift(GoRustBuffer{
				inner: status.errorBuf,
			})))
		} else {
			panic(fmt.Errorf("Rust panicked while handling Rust panic"))
		}
	default:
		return fmt.Errorf("unknown status code: %d", status.code)
	}
}

func rustCall[U any](callback func(*C.RustCallStatus) U) U {
	returnValue, err := rustCallWithError[error](nil, callback)
	if err != nil {
		panic(err)
	}
	return returnValue
}

type NativeError interface {
	AsError() error
}

func writeInt8(writer io.Writer, value int8) {
	if err := binary.Write(writer, binary.BigEndian, value); err != nil {
		panic(err)
	}
}

func writeUint8(writer io.Writer, value uint8) {
	if err := binary.Write(writer, binary.BigEndian, value); err != nil {
		panic(err)
	}
}

func writeInt16(writer io.Writer, value int16) {
	if err := binary.Write(writer, binary.BigEndian, value); err != nil {
		panic(err)
	}
}

func writeUint16(writer io.Writer, value uint16) {
	if err := binary.Write(writer, binary.BigEndian, value); err != nil {
		panic(err)
	}
}

func writeInt32(writer io.Writer, value int32) {
	if err := binary.Write(writer, binary.BigEndian, value); err != nil {
		panic(err)
	}
}

func writeUint32(writer io.Writer, value uint32) {
	if err := binary.Write(writer, binary.BigEndian, value); err != nil {
		panic(err)
	}
}

func writeInt64(writer io.Writer, value int64) {
	if err := binary.Write(writer, binary.BigEndian, value); err != nil {
		panic(err)
	}
}

func writeUint64(writer io.Writer, value uint64) {
	if err := binary.Write(writer, binary.BigEndian, value); err != nil {
		panic(err)
	}
}

func writeFloat32(writer io.Writer, value float32) {
	if err := binary.Write(writer, binary.BigEndian, value); err != nil {
		panic(err)
	}
}

func writeFloat64(writer io.Writer, value float64) {
	if err := binary.Write(writer, binary.BigEndian, value); err != nil {
		panic(err)
	}
}

func readInt8(reader io.Reader) int8 {
	var result int8
	if err := binary.Read(reader, binary.BigEndian, &result); err != nil {
		panic(err)
	}
	return result
}

func readUint8(reader io.Reader) uint8 {
	var result uint8
	if err := binary.Read(reader, binary.BigEndian, &result); err != nil {
		panic(err)
	}
	return result
}

func readInt16(reader io.Reader) int16 {
	var result int16
	if err := binary.Read(reader, binary.BigEndian, &result); err != nil {
		panic(err)
	}
	return result
}

func readUint16(reader io.Reader) uint16 {
	var result uint16
	if err := binary.Read(reader, binary.BigEndian, &result); err != nil {
		panic(err)
	}
	return result
}

func readInt32(reader io.Reader) int32 {
	var result int32
	if err := binary.Read(reader, binary.BigEndian, &result); err != nil {
		panic(err)
	}
	return result
}

func readUint32(reader io.Reader) uint32 {
	var result uint32
	if err := binary.Read(reader, binary.BigEndian, &result); err != nil {
		panic(err)
	}
	return result
}

func readInt64(reader io.Reader) int64 {
	var result int64
	if err := binary.Read(reader, binary.BigEndian, &result); err != nil {
		panic(err)
	}
	return result
}

func readUint64(reader io.Reader) uint64 {
	var result uint64
	if err := binary.Read(reader, binary.BigEndian, &result); err != nil {
		panic(err)
	}
	return result
}

func readFloat32(reader io.Reader) float32 {
	var result float32
	if err := binary.Read(reader, binary.BigEndian, &result); err != nil {
		panic(err)
	}
	return result
}

func readFloat64(reader io.Reader) float64 {
	var result float64
	if err := binary.Read(reader, binary.BigEndian, &result); err != nil {
		panic(err)
	}
	return result
}

func init() {

	FfiConverterFullScanScriptInspectorINSTANCE.register()
	FfiConverterSyncScriptInspectorINSTANCE.register()
	uniffiCheckChecksums()
}

func uniffiCheckChecksums() {
	// Get the bindings contract version from our ComponentInterface
	bindingsContractVersion := 29
	// Get the scaffolding contract version by calling the into the dylib
	scaffoldingContractVersion := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint32_t {
		return C.ffi_bdkffi_uniffi_contract_version()
	})
	if bindingsContractVersion != int(scaffoldingContractVersion) {
		// If this happens try cleaning and rebuilding your project
		panic("bdk: UniFFI contract version mismatch")
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_method_address_is_valid_for_network()
		})
		if checksum != 10350 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_method_address_is_valid_for_network: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_method_address_script_pubkey()
		})
		if checksum != 10722 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_method_address_script_pubkey: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_method_address_to_address_data()
		})
		if checksum != 61625 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_method_address_to_address_data: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_method_address_to_qr_uri()
		})
		if checksum != 48141 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_method_address_to_qr_uri: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_method_amount_to_btc()
		})
		if checksum != 52662 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_method_amount_to_btc: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_method_amount_to_sat()
		})
		if checksum != 54936 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_method_amount_to_sat: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_method_bumpfeetxbuilder_allow_dust()
		})
		if checksum != 17791 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_method_bumpfeetxbuilder_allow_dust: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_method_bumpfeetxbuilder_current_height()
		})
		if checksum != 7420 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_method_bumpfeetxbuilder_current_height: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_method_bumpfeetxbuilder_finish()
		})
		if checksum != 18299 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_method_bumpfeetxbuilder_finish: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_method_bumpfeetxbuilder_nlocktime()
		})
		if checksum != 11468 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_method_bumpfeetxbuilder_nlocktime: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_method_bumpfeetxbuilder_set_exact_sequence()
		})
		if checksum != 35609 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_method_bumpfeetxbuilder_set_exact_sequence: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_method_bumpfeetxbuilder_version()
		})
		if checksum != 4756 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_method_bumpfeetxbuilder_version: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_method_descriptor_is_multipath()
		})
		if checksum != 3912 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_method_descriptor_is_multipath: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_method_descriptor_to_single_descriptors()
		})
		if checksum != 33905 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_method_descriptor_to_single_descriptors: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_method_descriptor_to_string_with_secret()
		})
		if checksum != 18986 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_method_descriptor_to_string_with_secret: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_method_descriptorpublickey_as_string()
		})
		if checksum != 37256 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_method_descriptorpublickey_as_string: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_method_descriptorpublickey_derive()
		})
		if checksum != 42652 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_method_descriptorpublickey_derive: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_method_descriptorpublickey_extend()
		})
		if checksum != 46128 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_method_descriptorpublickey_extend: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_method_descriptorpublickey_is_multipath()
		})
		if checksum != 45386 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_method_descriptorpublickey_is_multipath: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_method_descriptorpublickey_master_fingerprint()
		})
		if checksum != 19753 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_method_descriptorpublickey_master_fingerprint: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_method_descriptorsecretkey_as_public()
		})
		if checksum != 56954 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_method_descriptorsecretkey_as_public: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_method_descriptorsecretkey_as_string()
		})
		if checksum != 28335 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_method_descriptorsecretkey_as_string: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_method_descriptorsecretkey_derive()
		})
		if checksum != 61335 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_method_descriptorsecretkey_derive: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_method_descriptorsecretkey_extend()
		})
		if checksum != 19969 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_method_descriptorsecretkey_extend: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_method_descriptorsecretkey_secret_bytes()
		})
		if checksum != 40876 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_method_descriptorsecretkey_secret_bytes: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_method_electrumclient_estimate_fee()
		})
		if checksum != 17604 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_method_electrumclient_estimate_fee: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_method_electrumclient_full_scan()
		})
		if checksum != 45625 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_method_electrumclient_full_scan: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_method_electrumclient_server_features()
		})
		if checksum != 18744 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_method_electrumclient_server_features: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_method_electrumclient_sync()
		})
		if checksum != 62150 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_method_electrumclient_sync: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_method_electrumclient_transaction_broadcast()
		})
		if checksum != 36923 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_method_electrumclient_transaction_broadcast: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_method_esploraclient_broadcast()
		})
		if checksum != 21200 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_method_esploraclient_broadcast: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_method_esploraclient_full_scan()
		})
		if checksum != 43201 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_method_esploraclient_full_scan: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_method_esploraclient_get_block_hash()
		})
		if checksum != 18600 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_method_esploraclient_get_block_hash: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_method_esploraclient_get_fee_estimates()
		})
		if checksum != 64331 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_method_esploraclient_get_fee_estimates: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_method_esploraclient_get_height()
		})
		if checksum != 1218 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_method_esploraclient_get_height: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_method_esploraclient_get_tx()
		})
		if checksum != 59770 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_method_esploraclient_get_tx: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_method_esploraclient_get_tx_info()
		})
		if checksum != 1114 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_method_esploraclient_get_tx_info: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_method_esploraclient_get_tx_status()
		})
		if checksum != 25440 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_method_esploraclient_get_tx_status: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_method_esploraclient_sync()
		})
		if checksum != 11965 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_method_esploraclient_sync: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_method_feerate_to_sat_per_kwu()
		})
		if checksum != 2433 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_method_feerate_to_sat_per_kwu: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_method_feerate_to_sat_per_vb_ceil()
		})
		if checksum != 21019 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_method_feerate_to_sat_per_vb_ceil: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_method_feerate_to_sat_per_vb_floor()
		})
		if checksum != 54438 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_method_feerate_to_sat_per_vb_floor: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_method_fullscanrequestbuilder_build()
		})
		if checksum != 56245 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_method_fullscanrequestbuilder_build: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_method_fullscanrequestbuilder_inspect_spks_for_all_keychains()
		})
		if checksum != 6853 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_method_fullscanrequestbuilder_inspect_spks_for_all_keychains: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_method_fullscanscriptinspector_inspect()
		})
		if checksum != 52426 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_method_fullscanscriptinspector_inspect: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_method_policy_as_string()
		})
		if checksum != 41785 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_method_policy_as_string: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_method_policy_contribution()
		})
		if checksum != 25625 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_method_policy_contribution: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_method_policy_id()
		})
		if checksum != 33085 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_method_policy_id: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_method_policy_item()
		})
		if checksum != 24039 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_method_policy_item: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_method_policy_requires_path()
		})
		if checksum != 40639 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_method_policy_requires_path: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_method_policy_satisfaction()
		})
		if checksum != 28647 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_method_policy_satisfaction: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_method_psbt_combine()
		})
		if checksum != 42218 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_method_psbt_combine: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_method_psbt_extract_tx()
		})
		if checksum != 60519 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_method_psbt_extract_tx: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_method_psbt_fee()
		})
		if checksum != 48877 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_method_psbt_fee: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_method_psbt_finalize()
		})
		if checksum != 20182 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_method_psbt_finalize: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_method_psbt_json_serialize()
		})
		if checksum != 9611 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_method_psbt_json_serialize: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_method_psbt_serialize()
		})
		if checksum != 33309 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_method_psbt_serialize: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_method_script_to_bytes()
		})
		if checksum != 31368 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_method_script_to_bytes: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_method_syncrequestbuilder_build()
		})
		if checksum != 38954 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_method_syncrequestbuilder_build: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_method_syncrequestbuilder_inspect_spks()
		})
		if checksum != 33029 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_method_syncrequestbuilder_inspect_spks: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_method_syncscriptinspector_inspect()
		})
		if checksum != 6883 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_method_syncscriptinspector_inspect: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_method_transaction_compute_txid()
		})
		if checksum != 46504 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_method_transaction_compute_txid: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_method_transaction_input()
		})
		if checksum != 5374 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_method_transaction_input: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_method_transaction_is_coinbase()
		})
		if checksum != 14454 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_method_transaction_is_coinbase: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_method_transaction_is_explicitly_rbf()
		})
		if checksum != 32682 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_method_transaction_is_explicitly_rbf: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_method_transaction_is_lock_time_enabled()
		})
		if checksum != 48885 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_method_transaction_is_lock_time_enabled: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_method_transaction_lock_time()
		})
		if checksum != 49321 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_method_transaction_lock_time: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_method_transaction_output()
		})
		if checksum != 30237 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_method_transaction_output: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_method_transaction_serialize()
		})
		if checksum != 62862 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_method_transaction_serialize: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_method_transaction_total_size()
		})
		if checksum != 12759 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_method_transaction_total_size: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_method_transaction_version()
		})
		if checksum != 15271 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_method_transaction_version: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_method_transaction_vsize()
		})
		if checksum != 3804 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_method_transaction_vsize: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_method_transaction_weight()
		})
		if checksum != 21879 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_method_transaction_weight: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_method_txbuilder_add_data()
		})
		if checksum != 7385 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_method_txbuilder_add_data: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_method_txbuilder_add_global_xpubs()
		})
		if checksum != 61114 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_method_txbuilder_add_global_xpubs: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_method_txbuilder_add_recipient()
		})
		if checksum != 2935 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_method_txbuilder_add_recipient: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_method_txbuilder_add_unspendable()
		})
		if checksum != 33319 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_method_txbuilder_add_unspendable: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_method_txbuilder_add_utxo()
		})
		if checksum != 43637 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_method_txbuilder_add_utxo: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_method_txbuilder_allow_dust()
		})
		if checksum != 14086 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_method_txbuilder_allow_dust: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_method_txbuilder_change_policy()
		})
		if checksum != 22333 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_method_txbuilder_change_policy: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_method_txbuilder_current_height()
		})
		if checksum != 54586 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_method_txbuilder_current_height: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_method_txbuilder_do_not_spend_change()
		})
		if checksum != 51770 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_method_txbuilder_do_not_spend_change: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_method_txbuilder_drain_to()
		})
		if checksum != 21128 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_method_txbuilder_drain_to: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_method_txbuilder_drain_wallet()
		})
		if checksum != 5081 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_method_txbuilder_drain_wallet: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_method_txbuilder_fee_absolute()
		})
		if checksum != 964 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_method_txbuilder_fee_absolute: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_method_txbuilder_fee_rate()
		})
		if checksum != 60371 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_method_txbuilder_fee_rate: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_method_txbuilder_finish()
		})
		if checksum != 61082 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_method_txbuilder_finish: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_method_txbuilder_manually_selected_only()
		})
		if checksum != 12623 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_method_txbuilder_manually_selected_only: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_method_txbuilder_nlocktime()
		})
		if checksum != 34620 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_method_txbuilder_nlocktime: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_method_txbuilder_only_spend_change()
		})
		if checksum != 18757 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_method_txbuilder_only_spend_change: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_method_txbuilder_policy_path()
		})
		if checksum != 45435 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_method_txbuilder_policy_path: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_method_txbuilder_set_exact_sequence()
		})
		if checksum != 35105 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_method_txbuilder_set_exact_sequence: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_method_txbuilder_set_recipients()
		})
		if checksum != 20461 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_method_txbuilder_set_recipients: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_method_txbuilder_unspendable()
		})
		if checksum != 49004 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_method_txbuilder_unspendable: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_method_txbuilder_version()
		})
		if checksum != 47401 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_method_txbuilder_version: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_method_wallet_apply_update()
		})
		if checksum != 65428 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_method_wallet_apply_update: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_method_wallet_balance()
		})
		if checksum != 32173 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_method_wallet_balance: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_method_wallet_calculate_fee()
		})
		if checksum != 14264 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_method_wallet_calculate_fee: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_method_wallet_calculate_fee_rate()
		})
		if checksum != 61555 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_method_wallet_calculate_fee_rate: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_method_wallet_cancel_tx()
		})
		if checksum != 27219 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_method_wallet_cancel_tx: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_method_wallet_derivation_index()
		})
		if checksum != 63084 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_method_wallet_derivation_index: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_method_wallet_derivation_of_spk()
		})
		if checksum != 48832 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_method_wallet_derivation_of_spk: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_method_wallet_descriptor_checksum()
		})
		if checksum != 60436 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_method_wallet_descriptor_checksum: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_method_wallet_finalize_psbt()
		})
		if checksum != 52988 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_method_wallet_finalize_psbt: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_method_wallet_get_tx()
		})
		if checksum != 59450 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_method_wallet_get_tx: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_method_wallet_get_utxo()
		})
		if checksum != 48342 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_method_wallet_get_utxo: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_method_wallet_is_mine()
		})
		if checksum != 56329 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_method_wallet_is_mine: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_method_wallet_list_output()
		})
		if checksum != 27359 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_method_wallet_list_output: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_method_wallet_list_unspent()
		})
		if checksum != 25643 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_method_wallet_list_unspent: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_method_wallet_list_unused_addresses()
		})
		if checksum != 1695 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_method_wallet_list_unused_addresses: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_method_wallet_mark_used()
		})
		if checksum != 51163 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_method_wallet_mark_used: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_method_wallet_network()
		})
		if checksum != 32197 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_method_wallet_network: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_method_wallet_next_derivation_index()
		})
		if checksum != 47127 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_method_wallet_next_derivation_index: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_method_wallet_next_unused_address()
		})
		if checksum != 18644 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_method_wallet_next_unused_address: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_method_wallet_peek_address()
		})
		if checksum != 47647 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_method_wallet_peek_address: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_method_wallet_persist()
		})
		if checksum != 14909 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_method_wallet_persist: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_method_wallet_policies()
		})
		if checksum != 23929 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_method_wallet_policies: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_method_wallet_reveal_addresses_to()
		})
		if checksum != 10653 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_method_wallet_reveal_addresses_to: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_method_wallet_reveal_next_address()
		})
		if checksum != 54031 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_method_wallet_reveal_next_address: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_method_wallet_sent_and_received()
		})
		if checksum != 15077 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_method_wallet_sent_and_received: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_method_wallet_sign()
		})
		if checksum != 41599 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_method_wallet_sign: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_method_wallet_start_full_scan()
		})
		if checksum != 3023 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_method_wallet_start_full_scan: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_method_wallet_start_sync_with_revealed_spks()
		})
		if checksum != 41977 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_method_wallet_start_sync_with_revealed_spks: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_method_wallet_transactions()
		})
		if checksum != 37950 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_method_wallet_transactions: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_constructor_address_from_script()
		})
		if checksum != 63028 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_constructor_address_from_script: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_constructor_address_new()
		})
		if checksum != 10014 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_constructor_address_new: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_constructor_amount_from_btc()
		})
		if checksum != 46865 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_constructor_amount_from_btc: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_constructor_amount_from_sat()
		})
		if checksum != 16600 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_constructor_amount_from_sat: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_constructor_bumpfeetxbuilder_new()
		})
		if checksum != 39896 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_constructor_bumpfeetxbuilder_new: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_constructor_connection_new()
		})
		if checksum != 57214 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_constructor_connection_new: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_constructor_connection_new_in_memory()
		})
		if checksum != 62138 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_constructor_connection_new_in_memory: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_constructor_derivationpath_new()
		})
		if checksum != 18379 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_constructor_derivationpath_new: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_constructor_descriptor_new()
		})
		if checksum != 19415 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_constructor_descriptor_new: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_constructor_descriptor_new_bip44()
		})
		if checksum != 640 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_constructor_descriptor_new_bip44: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_constructor_descriptor_new_bip44_public()
		})
		if checksum != 17163 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_constructor_descriptor_new_bip44_public: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_constructor_descriptor_new_bip49()
		})
		if checksum != 50215 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_constructor_descriptor_new_bip49: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_constructor_descriptor_new_bip49_public()
		})
		if checksum != 16648 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_constructor_descriptor_new_bip49_public: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_constructor_descriptor_new_bip84()
		})
		if checksum != 56174 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_constructor_descriptor_new_bip84: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_constructor_descriptor_new_bip84_public()
		})
		if checksum != 27707 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_constructor_descriptor_new_bip84_public: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_constructor_descriptor_new_bip86()
		})
		if checksum != 52779 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_constructor_descriptor_new_bip86: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_constructor_descriptor_new_bip86_public()
		})
		if checksum != 60138 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_constructor_descriptor_new_bip86_public: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_constructor_descriptorpublickey_from_string()
		})
		if checksum != 13510 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_constructor_descriptorpublickey_from_string: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_constructor_descriptorsecretkey_from_string()
		})
		if checksum != 35137 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_constructor_descriptorsecretkey_from_string: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_constructor_descriptorsecretkey_new()
		})
		if checksum != 516 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_constructor_descriptorsecretkey_new: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_constructor_electrumclient_new()
		})
		if checksum != 2443 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_constructor_electrumclient_new: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_constructor_esploraclient_new()
		})
		if checksum != 48470 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_constructor_esploraclient_new: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_constructor_feerate_from_sat_per_kwu()
		})
		if checksum != 23637 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_constructor_feerate_from_sat_per_kwu: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_constructor_feerate_from_sat_per_vb()
		})
		if checksum != 39844 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_constructor_feerate_from_sat_per_vb: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_constructor_mnemonic_from_entropy()
		})
		if checksum != 30681 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_constructor_mnemonic_from_entropy: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_constructor_mnemonic_from_string()
		})
		if checksum != 22177 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_constructor_mnemonic_from_string: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_constructor_mnemonic_new()
		})
		if checksum != 62260 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_constructor_mnemonic_new: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_constructor_psbt_new()
		})
		if checksum != 34802 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_constructor_psbt_new: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_constructor_script_new()
		})
		if checksum != 54110 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_constructor_script_new: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_constructor_transaction_new()
		})
		if checksum != 52808 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_constructor_transaction_new: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_constructor_txbuilder_new()
		})
		if checksum != 60455 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_constructor_txbuilder_new: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_constructor_wallet_load()
		})
		if checksum != 49712 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_constructor_wallet_load: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_bdkffi_checksum_constructor_wallet_new()
		})
		if checksum != 30052 {
			// If this happens try cleaning and rebuilding your project
			panic("bdk: uniffi_bdkffi_checksum_constructor_wallet_new: UniFFI API checksum mismatch")
		}
	}
}

type FfiConverterUint8 struct{}

var FfiConverterUint8INSTANCE = FfiConverterUint8{}

func (FfiConverterUint8) Lower(value uint8) C.uint8_t {
	return C.uint8_t(value)
}

func (FfiConverterUint8) Write(writer io.Writer, value uint8) {
	writeUint8(writer, value)
}

func (FfiConverterUint8) Lift(value C.uint8_t) uint8 {
	return uint8(value)
}

func (FfiConverterUint8) Read(reader io.Reader) uint8 {
	return readUint8(reader)
}

type FfiDestroyerUint8 struct{}

func (FfiDestroyerUint8) Destroy(_ uint8) {}

type FfiConverterUint16 struct{}

var FfiConverterUint16INSTANCE = FfiConverterUint16{}

func (FfiConverterUint16) Lower(value uint16) C.uint16_t {
	return C.uint16_t(value)
}

func (FfiConverterUint16) Write(writer io.Writer, value uint16) {
	writeUint16(writer, value)
}

func (FfiConverterUint16) Lift(value C.uint16_t) uint16 {
	return uint16(value)
}

func (FfiConverterUint16) Read(reader io.Reader) uint16 {
	return readUint16(reader)
}

type FfiDestroyerUint16 struct{}

func (FfiDestroyerUint16) Destroy(_ uint16) {}

type FfiConverterUint32 struct{}

var FfiConverterUint32INSTANCE = FfiConverterUint32{}

func (FfiConverterUint32) Lower(value uint32) C.uint32_t {
	return C.uint32_t(value)
}

func (FfiConverterUint32) Write(writer io.Writer, value uint32) {
	writeUint32(writer, value)
}

func (FfiConverterUint32) Lift(value C.uint32_t) uint32 {
	return uint32(value)
}

func (FfiConverterUint32) Read(reader io.Reader) uint32 {
	return readUint32(reader)
}

type FfiDestroyerUint32 struct{}

func (FfiDestroyerUint32) Destroy(_ uint32) {}

type FfiConverterInt32 struct{}

var FfiConverterInt32INSTANCE = FfiConverterInt32{}

func (FfiConverterInt32) Lower(value int32) C.int32_t {
	return C.int32_t(value)
}

func (FfiConverterInt32) Write(writer io.Writer, value int32) {
	writeInt32(writer, value)
}

func (FfiConverterInt32) Lift(value C.int32_t) int32 {
	return int32(value)
}

func (FfiConverterInt32) Read(reader io.Reader) int32 {
	return readInt32(reader)
}

type FfiDestroyerInt32 struct{}

func (FfiDestroyerInt32) Destroy(_ int32) {}

type FfiConverterUint64 struct{}

var FfiConverterUint64INSTANCE = FfiConverterUint64{}

func (FfiConverterUint64) Lower(value uint64) C.uint64_t {
	return C.uint64_t(value)
}

func (FfiConverterUint64) Write(writer io.Writer, value uint64) {
	writeUint64(writer, value)
}

func (FfiConverterUint64) Lift(value C.uint64_t) uint64 {
	return uint64(value)
}

func (FfiConverterUint64) Read(reader io.Reader) uint64 {
	return readUint64(reader)
}

type FfiDestroyerUint64 struct{}

func (FfiDestroyerUint64) Destroy(_ uint64) {}

type FfiConverterInt64 struct{}

var FfiConverterInt64INSTANCE = FfiConverterInt64{}

func (FfiConverterInt64) Lower(value int64) C.int64_t {
	return C.int64_t(value)
}

func (FfiConverterInt64) Write(writer io.Writer, value int64) {
	writeInt64(writer, value)
}

func (FfiConverterInt64) Lift(value C.int64_t) int64 {
	return int64(value)
}

func (FfiConverterInt64) Read(reader io.Reader) int64 {
	return readInt64(reader)
}

type FfiDestroyerInt64 struct{}

func (FfiDestroyerInt64) Destroy(_ int64) {}

type FfiConverterFloat64 struct{}

var FfiConverterFloat64INSTANCE = FfiConverterFloat64{}

func (FfiConverterFloat64) Lower(value float64) C.double {
	return C.double(value)
}

func (FfiConverterFloat64) Write(writer io.Writer, value float64) {
	writeFloat64(writer, value)
}

func (FfiConverterFloat64) Lift(value C.double) float64 {
	return float64(value)
}

func (FfiConverterFloat64) Read(reader io.Reader) float64 {
	return readFloat64(reader)
}

type FfiDestroyerFloat64 struct{}

func (FfiDestroyerFloat64) Destroy(_ float64) {}

type FfiConverterBool struct{}

var FfiConverterBoolINSTANCE = FfiConverterBool{}

func (FfiConverterBool) Lower(value bool) C.int8_t {
	if value {
		return C.int8_t(1)
	}
	return C.int8_t(0)
}

func (FfiConverterBool) Write(writer io.Writer, value bool) {
	if value {
		writeInt8(writer, 1)
	} else {
		writeInt8(writer, 0)
	}
}

func (FfiConverterBool) Lift(value C.int8_t) bool {
	return value != 0
}

func (FfiConverterBool) Read(reader io.Reader) bool {
	return readInt8(reader) != 0
}

type FfiDestroyerBool struct{}

func (FfiDestroyerBool) Destroy(_ bool) {}

type FfiConverterString struct{}

var FfiConverterStringINSTANCE = FfiConverterString{}

func (FfiConverterString) Lift(rb RustBufferI) string {
	defer rb.Free()
	reader := rb.AsReader()
	b, err := io.ReadAll(reader)
	if err != nil {
		panic(fmt.Errorf("reading reader: %w", err))
	}
	return string(b)
}

func (FfiConverterString) Read(reader io.Reader) string {
	length := readInt32(reader)
	buffer := make([]byte, length)
	read_length, err := reader.Read(buffer)
	if err != nil && err != io.EOF {
		panic(err)
	}
	if read_length != int(length) {
		panic(fmt.Errorf("bad read length when reading string, expected %d, read %d", length, read_length))
	}
	return string(buffer)
}

func (FfiConverterString) Lower(value string) C.RustBuffer {
	return stringToRustBuffer(value)
}

func (FfiConverterString) Write(writer io.Writer, value string) {
	if len(value) > math.MaxInt32 {
		panic("String is too large to fit into Int32")
	}

	writeInt32(writer, int32(len(value)))
	write_length, err := io.WriteString(writer, value)
	if err != nil {
		panic(err)
	}
	if write_length != len(value) {
		panic(fmt.Errorf("bad write length when writing string, expected %d, written %d", len(value), write_length))
	}
}

type FfiDestroyerString struct{}

func (FfiDestroyerString) Destroy(_ string) {}

// Below is an implementation of synchronization requirements outlined in the link.
// https://github.com/mozilla/uniffi-rs/blob/0dc031132d9493ca812c3af6e7dd60ad2ea95bf0/uniffi_bindgen/src/bindings/kotlin/templates/ObjectRuntime.kt#L31

type FfiObject struct {
	pointer       unsafe.Pointer
	callCounter   atomic.Int64
	cloneFunction func(unsafe.Pointer, *C.RustCallStatus) unsafe.Pointer
	freeFunction  func(unsafe.Pointer, *C.RustCallStatus)
	destroyed     atomic.Bool
}

func newFfiObject(
	pointer unsafe.Pointer,
	cloneFunction func(unsafe.Pointer, *C.RustCallStatus) unsafe.Pointer,
	freeFunction func(unsafe.Pointer, *C.RustCallStatus),
) FfiObject {
	return FfiObject{
		pointer:       pointer,
		cloneFunction: cloneFunction,
		freeFunction:  freeFunction,
	}
}

func (ffiObject *FfiObject) incrementPointer(debugName string) unsafe.Pointer {
	for {
		counter := ffiObject.callCounter.Load()
		if counter <= -1 {
			panic(fmt.Errorf("%v object has already been destroyed", debugName))
		}
		if counter == math.MaxInt64 {
			panic(fmt.Errorf("%v object call counter would overflow", debugName))
		}
		if ffiObject.callCounter.CompareAndSwap(counter, counter+1) {
			break
		}
	}

	return rustCall(func(status *C.RustCallStatus) unsafe.Pointer {
		return ffiObject.cloneFunction(ffiObject.pointer, status)
	})
}

func (ffiObject *FfiObject) decrementPointer() {
	if ffiObject.callCounter.Add(-1) == -1 {
		ffiObject.freeRustArcPtr()
	}
}

func (ffiObject *FfiObject) destroy() {
	if ffiObject.destroyed.CompareAndSwap(false, true) {
		if ffiObject.callCounter.Add(-1) == -1 {
			ffiObject.freeRustArcPtr()
		}
	}
}

func (ffiObject *FfiObject) freeRustArcPtr() {
	rustCall(func(status *C.RustCallStatus) int32 {
		ffiObject.freeFunction(ffiObject.pointer, status)
		return 0
	})
}

type AddressInterface interface {
	IsValidForNetwork(network Network) bool
	ScriptPubkey() *Script
	ToAddressData() AddressData
	ToQrUri() string
}
type Address struct {
	ffiObject FfiObject
}

func NewAddress(address string, network Network) (*Address, error) {
	_uniffiRV, _uniffiErr := rustCallWithError[AddressParseError](FfiConverterAddressParseError{}, func(_uniffiStatus *C.RustCallStatus) unsafe.Pointer {
		return C.uniffi_bdkffi_fn_constructor_address_new(FfiConverterStringINSTANCE.Lower(address), FfiConverterNetworkINSTANCE.Lower(network), _uniffiStatus)
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue *Address
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterAddressINSTANCE.Lift(_uniffiRV), nil
	}
}

func AddressFromScript(script *Script, network Network) (*Address, error) {
	_uniffiRV, _uniffiErr := rustCallWithError[FromScriptError](FfiConverterFromScriptError{}, func(_uniffiStatus *C.RustCallStatus) unsafe.Pointer {
		return C.uniffi_bdkffi_fn_constructor_address_from_script(FfiConverterScriptINSTANCE.Lower(script), FfiConverterNetworkINSTANCE.Lower(network), _uniffiStatus)
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue *Address
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterAddressINSTANCE.Lift(_uniffiRV), nil
	}
}

func (_self *Address) IsValidForNetwork(network Network) bool {
	_pointer := _self.ffiObject.incrementPointer("*Address")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterBoolINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) C.int8_t {
		return C.uniffi_bdkffi_fn_method_address_is_valid_for_network(
			_pointer, FfiConverterNetworkINSTANCE.Lower(network), _uniffiStatus)
	}))
}

func (_self *Address) ScriptPubkey() *Script {
	_pointer := _self.ffiObject.incrementPointer("*Address")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterScriptINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) unsafe.Pointer {
		return C.uniffi_bdkffi_fn_method_address_script_pubkey(
			_pointer, _uniffiStatus)
	}))
}

func (_self *Address) ToAddressData() AddressData {
	_pointer := _self.ffiObject.incrementPointer("*Address")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterAddressDataINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_bdkffi_fn_method_address_to_address_data(
				_pointer, _uniffiStatus),
		}
	}))
}

func (_self *Address) ToQrUri() string {
	_pointer := _self.ffiObject.incrementPointer("*Address")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterStringINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_bdkffi_fn_method_address_to_qr_uri(
				_pointer, _uniffiStatus),
		}
	}))
}

func (_self *Address) String() string {
	_pointer := _self.ffiObject.incrementPointer("*Address")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterStringINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_bdkffi_fn_method_address_uniffi_trait_display(
				_pointer, _uniffiStatus),
		}
	}))
}

func (object *Address) Destroy() {
	runtime.SetFinalizer(object, nil)
	object.ffiObject.destroy()
}

type FfiConverterAddress struct{}

var FfiConverterAddressINSTANCE = FfiConverterAddress{}

func (c FfiConverterAddress) Lift(pointer unsafe.Pointer) *Address {
	result := &Address{
		newFfiObject(
			pointer,
			func(pointer unsafe.Pointer, status *C.RustCallStatus) unsafe.Pointer {
				return C.uniffi_bdkffi_fn_clone_address(pointer, status)
			},
			func(pointer unsafe.Pointer, status *C.RustCallStatus) {
				C.uniffi_bdkffi_fn_free_address(pointer, status)
			},
		),
	}
	runtime.SetFinalizer(result, (*Address).Destroy)
	return result
}

func (c FfiConverterAddress) Read(reader io.Reader) *Address {
	return c.Lift(unsafe.Pointer(uintptr(readUint64(reader))))
}

func (c FfiConverterAddress) Lower(value *Address) unsafe.Pointer {
	// TODO: this is bad - all synchronization from ObjectRuntime.go is discarded here,
	// because the pointer will be decremented immediately after this function returns,
	// and someone will be left holding onto a non-locked pointer.
	pointer := value.ffiObject.incrementPointer("*Address")
	defer value.ffiObject.decrementPointer()
	return pointer

}

func (c FfiConverterAddress) Write(writer io.Writer, value *Address) {
	writeUint64(writer, uint64(uintptr(c.Lower(value))))
}

type FfiDestroyerAddress struct{}

func (_ FfiDestroyerAddress) Destroy(value *Address) {
	value.Destroy()
}

type AmountInterface interface {
	ToBtc() float64
	ToSat() uint64
}
type Amount struct {
	ffiObject FfiObject
}

func AmountFromBtc(fromBtc float64) (*Amount, error) {
	_uniffiRV, _uniffiErr := rustCallWithError[ParseAmountError](FfiConverterParseAmountError{}, func(_uniffiStatus *C.RustCallStatus) unsafe.Pointer {
		return C.uniffi_bdkffi_fn_constructor_amount_from_btc(FfiConverterFloat64INSTANCE.Lower(fromBtc), _uniffiStatus)
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue *Amount
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterAmountINSTANCE.Lift(_uniffiRV), nil
	}
}

func AmountFromSat(fromSat uint64) *Amount {
	return FfiConverterAmountINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) unsafe.Pointer {
		return C.uniffi_bdkffi_fn_constructor_amount_from_sat(FfiConverterUint64INSTANCE.Lower(fromSat), _uniffiStatus)
	}))
}

func (_self *Amount) ToBtc() float64 {
	_pointer := _self.ffiObject.incrementPointer("*Amount")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterFloat64INSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) C.double {
		return C.uniffi_bdkffi_fn_method_amount_to_btc(
			_pointer, _uniffiStatus)
	}))
}

func (_self *Amount) ToSat() uint64 {
	_pointer := _self.ffiObject.incrementPointer("*Amount")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterUint64INSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint64_t {
		return C.uniffi_bdkffi_fn_method_amount_to_sat(
			_pointer, _uniffiStatus)
	}))
}
func (object *Amount) Destroy() {
	runtime.SetFinalizer(object, nil)
	object.ffiObject.destroy()
}

type FfiConverterAmount struct{}

var FfiConverterAmountINSTANCE = FfiConverterAmount{}

func (c FfiConverterAmount) Lift(pointer unsafe.Pointer) *Amount {
	result := &Amount{
		newFfiObject(
			pointer,
			func(pointer unsafe.Pointer, status *C.RustCallStatus) unsafe.Pointer {
				return C.uniffi_bdkffi_fn_clone_amount(pointer, status)
			},
			func(pointer unsafe.Pointer, status *C.RustCallStatus) {
				C.uniffi_bdkffi_fn_free_amount(pointer, status)
			},
		),
	}
	runtime.SetFinalizer(result, (*Amount).Destroy)
	return result
}

func (c FfiConverterAmount) Read(reader io.Reader) *Amount {
	return c.Lift(unsafe.Pointer(uintptr(readUint64(reader))))
}

func (c FfiConverterAmount) Lower(value *Amount) unsafe.Pointer {
	// TODO: this is bad - all synchronization from ObjectRuntime.go is discarded here,
	// because the pointer will be decremented immediately after this function returns,
	// and someone will be left holding onto a non-locked pointer.
	pointer := value.ffiObject.incrementPointer("*Amount")
	defer value.ffiObject.decrementPointer()
	return pointer

}

func (c FfiConverterAmount) Write(writer io.Writer, value *Amount) {
	writeUint64(writer, uint64(uintptr(c.Lower(value))))
}

type FfiDestroyerAmount struct{}

func (_ FfiDestroyerAmount) Destroy(value *Amount) {
	value.Destroy()
}

// A `BumpFeeTxBuilder` is created by calling `build_fee_bump` on a wallet. After assigning it, you set options on it
// until finally calling `finish` to consume the builder and generate the transaction.
type BumpFeeTxBuilderInterface interface {
	// Set whether or not the dust limit is checked.
	//
	// Note: by avoiding a dust limit check you may end up with a transaction that is non-standard.
	AllowDust(allowDust bool) *BumpFeeTxBuilder
	// Set the current blockchain height.
	//
	// This will be used to:
	//
	// 1. Set the `nLockTime` for preventing fee sniping. Note: This will be ignored if you manually specify a
	// `nlocktime` using `TxBuilder::nlocktime`.
	//
	// 2. Decide whether coinbase outputs are mature or not. If the coinbase outputs are not mature at `current_height`,
	// we ignore them in the coin selection. If you want to create a transaction that spends immature coinbase inputs,
	// manually add them using `TxBuilder::add_utxos`.
	// In both cases, if you don’t provide a current height, we use the last sync height.
	CurrentHeight(height uint32) *BumpFeeTxBuilder
	// Finish building the transaction.
	//
	// Uses the thread-local random number generator (rng).
	//
	// Returns a new `Psbt` per BIP174.
	//
	// WARNING: To avoid change address reuse you must persist the changes resulting from one or more calls to this
	// method before closing the wallet. See `Wallet::reveal_next_address`.
	Finish(wallet *Wallet) (*Psbt, error)
	// Use a specific nLockTime while creating the transaction.
	//
	// This can cause conflicts if the wallet’s descriptors contain an "after" (`OP_CLTV`) operator.
	Nlocktime(locktime LockTime) *BumpFeeTxBuilder
	// Set an exact `nSequence` value.
	//
	// This can cause conflicts if the wallet’s descriptors contain an "older" (`OP_CSV`) operator and the given
	// `nsequence` is lower than the CSV value.
	SetExactSequence(nsequence uint32) *BumpFeeTxBuilder
	// Build a transaction with a specific version.
	//
	// The version should always be greater than 0 and greater than 1 if the wallet’s descriptors contain an "older"
	// (`OP_CSV`) operator.
	Version(version int32) *BumpFeeTxBuilder
}

// A `BumpFeeTxBuilder` is created by calling `build_fee_bump` on a wallet. After assigning it, you set options on it
// until finally calling `finish` to consume the builder and generate the transaction.
type BumpFeeTxBuilder struct {
	ffiObject FfiObject
}

func NewBumpFeeTxBuilder(txid string, feeRate *FeeRate) *BumpFeeTxBuilder {
	return FfiConverterBumpFeeTxBuilderINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) unsafe.Pointer {
		return C.uniffi_bdkffi_fn_constructor_bumpfeetxbuilder_new(FfiConverterStringINSTANCE.Lower(txid), FfiConverterFeeRateINSTANCE.Lower(feeRate), _uniffiStatus)
	}))
}

// Set whether or not the dust limit is checked.
//
// Note: by avoiding a dust limit check you may end up with a transaction that is non-standard.
func (_self *BumpFeeTxBuilder) AllowDust(allowDust bool) *BumpFeeTxBuilder {
	_pointer := _self.ffiObject.incrementPointer("*BumpFeeTxBuilder")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterBumpFeeTxBuilderINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) unsafe.Pointer {
		return C.uniffi_bdkffi_fn_method_bumpfeetxbuilder_allow_dust(
			_pointer, FfiConverterBoolINSTANCE.Lower(allowDust), _uniffiStatus)
	}))
}

// Set the current blockchain height.
//
// This will be used to:
//
// 1. Set the `nLockTime` for preventing fee sniping. Note: This will be ignored if you manually specify a
// `nlocktime` using `TxBuilder::nlocktime`.
//
// 2. Decide whether coinbase outputs are mature or not. If the coinbase outputs are not mature at `current_height`,
// we ignore them in the coin selection. If you want to create a transaction that spends immature coinbase inputs,
// manually add them using `TxBuilder::add_utxos`.
// In both cases, if you don’t provide a current height, we use the last sync height.
func (_self *BumpFeeTxBuilder) CurrentHeight(height uint32) *BumpFeeTxBuilder {
	_pointer := _self.ffiObject.incrementPointer("*BumpFeeTxBuilder")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterBumpFeeTxBuilderINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) unsafe.Pointer {
		return C.uniffi_bdkffi_fn_method_bumpfeetxbuilder_current_height(
			_pointer, FfiConverterUint32INSTANCE.Lower(height), _uniffiStatus)
	}))
}

// Finish building the transaction.
//
// Uses the thread-local random number generator (rng).
//
// Returns a new `Psbt` per BIP174.
//
// WARNING: To avoid change address reuse you must persist the changes resulting from one or more calls to this
// method before closing the wallet. See `Wallet::reveal_next_address`.
func (_self *BumpFeeTxBuilder) Finish(wallet *Wallet) (*Psbt, error) {
	_pointer := _self.ffiObject.incrementPointer("*BumpFeeTxBuilder")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[CreateTxError](FfiConverterCreateTxError{}, func(_uniffiStatus *C.RustCallStatus) unsafe.Pointer {
		return C.uniffi_bdkffi_fn_method_bumpfeetxbuilder_finish(
			_pointer, FfiConverterWalletINSTANCE.Lower(wallet), _uniffiStatus)
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue *Psbt
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterPsbtINSTANCE.Lift(_uniffiRV), nil
	}
}

// Use a specific nLockTime while creating the transaction.
//
// This can cause conflicts if the wallet’s descriptors contain an "after" (`OP_CLTV`) operator.
func (_self *BumpFeeTxBuilder) Nlocktime(locktime LockTime) *BumpFeeTxBuilder {
	_pointer := _self.ffiObject.incrementPointer("*BumpFeeTxBuilder")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterBumpFeeTxBuilderINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) unsafe.Pointer {
		return C.uniffi_bdkffi_fn_method_bumpfeetxbuilder_nlocktime(
			_pointer, FfiConverterLockTimeINSTANCE.Lower(locktime), _uniffiStatus)
	}))
}

// Set an exact `nSequence` value.
//
// This can cause conflicts if the wallet’s descriptors contain an "older" (`OP_CSV`) operator and the given
// `nsequence` is lower than the CSV value.
func (_self *BumpFeeTxBuilder) SetExactSequence(nsequence uint32) *BumpFeeTxBuilder {
	_pointer := _self.ffiObject.incrementPointer("*BumpFeeTxBuilder")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterBumpFeeTxBuilderINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) unsafe.Pointer {
		return C.uniffi_bdkffi_fn_method_bumpfeetxbuilder_set_exact_sequence(
			_pointer, FfiConverterUint32INSTANCE.Lower(nsequence), _uniffiStatus)
	}))
}

// Build a transaction with a specific version.
//
// The version should always be greater than 0 and greater than 1 if the wallet’s descriptors contain an "older"
// (`OP_CSV`) operator.
func (_self *BumpFeeTxBuilder) Version(version int32) *BumpFeeTxBuilder {
	_pointer := _self.ffiObject.incrementPointer("*BumpFeeTxBuilder")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterBumpFeeTxBuilderINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) unsafe.Pointer {
		return C.uniffi_bdkffi_fn_method_bumpfeetxbuilder_version(
			_pointer, FfiConverterInt32INSTANCE.Lower(version), _uniffiStatus)
	}))
}
func (object *BumpFeeTxBuilder) Destroy() {
	runtime.SetFinalizer(object, nil)
	object.ffiObject.destroy()
}

type FfiConverterBumpFeeTxBuilder struct{}

var FfiConverterBumpFeeTxBuilderINSTANCE = FfiConverterBumpFeeTxBuilder{}

func (c FfiConverterBumpFeeTxBuilder) Lift(pointer unsafe.Pointer) *BumpFeeTxBuilder {
	result := &BumpFeeTxBuilder{
		newFfiObject(
			pointer,
			func(pointer unsafe.Pointer, status *C.RustCallStatus) unsafe.Pointer {
				return C.uniffi_bdkffi_fn_clone_bumpfeetxbuilder(pointer, status)
			},
			func(pointer unsafe.Pointer, status *C.RustCallStatus) {
				C.uniffi_bdkffi_fn_free_bumpfeetxbuilder(pointer, status)
			},
		),
	}
	runtime.SetFinalizer(result, (*BumpFeeTxBuilder).Destroy)
	return result
}

func (c FfiConverterBumpFeeTxBuilder) Read(reader io.Reader) *BumpFeeTxBuilder {
	return c.Lift(unsafe.Pointer(uintptr(readUint64(reader))))
}

func (c FfiConverterBumpFeeTxBuilder) Lower(value *BumpFeeTxBuilder) unsafe.Pointer {
	// TODO: this is bad - all synchronization from ObjectRuntime.go is discarded here,
	// because the pointer will be decremented immediately after this function returns,
	// and someone will be left holding onto a non-locked pointer.
	pointer := value.ffiObject.incrementPointer("*BumpFeeTxBuilder")
	defer value.ffiObject.decrementPointer()
	return pointer

}

func (c FfiConverterBumpFeeTxBuilder) Write(writer io.Writer, value *BumpFeeTxBuilder) {
	writeUint64(writer, uint64(uintptr(c.Lower(value))))
}

type FfiDestroyerBumpFeeTxBuilder struct{}

func (_ FfiDestroyerBumpFeeTxBuilder) Destroy(value *BumpFeeTxBuilder) {
	value.Destroy()
}

// A changeset for [`Wallet`].
type ChangeSetInterface interface {
}

// A changeset for [`Wallet`].
type ChangeSet struct {
	ffiObject FfiObject
}

func (object *ChangeSet) Destroy() {
	runtime.SetFinalizer(object, nil)
	object.ffiObject.destroy()
}

type FfiConverterChangeSet struct{}

var FfiConverterChangeSetINSTANCE = FfiConverterChangeSet{}

func (c FfiConverterChangeSet) Lift(pointer unsafe.Pointer) *ChangeSet {
	result := &ChangeSet{
		newFfiObject(
			pointer,
			func(pointer unsafe.Pointer, status *C.RustCallStatus) unsafe.Pointer {
				return C.uniffi_bdkffi_fn_clone_changeset(pointer, status)
			},
			func(pointer unsafe.Pointer, status *C.RustCallStatus) {
				C.uniffi_bdkffi_fn_free_changeset(pointer, status)
			},
		),
	}
	runtime.SetFinalizer(result, (*ChangeSet).Destroy)
	return result
}

func (c FfiConverterChangeSet) Read(reader io.Reader) *ChangeSet {
	return c.Lift(unsafe.Pointer(uintptr(readUint64(reader))))
}

func (c FfiConverterChangeSet) Lower(value *ChangeSet) unsafe.Pointer {
	// TODO: this is bad - all synchronization from ObjectRuntime.go is discarded here,
	// because the pointer will be decremented immediately after this function returns,
	// and someone will be left holding onto a non-locked pointer.
	pointer := value.ffiObject.incrementPointer("*ChangeSet")
	defer value.ffiObject.decrementPointer()
	return pointer

}

func (c FfiConverterChangeSet) Write(writer io.Writer, value *ChangeSet) {
	writeUint64(writer, uint64(uintptr(c.Lower(value))))
}

type FfiDestroyerChangeSet struct{}

func (_ FfiDestroyerChangeSet) Destroy(value *ChangeSet) {
	value.Destroy()
}

type ConnectionInterface interface {
}
type Connection struct {
	ffiObject FfiObject
}

func NewConnection(path string) (*Connection, error) {
	_uniffiRV, _uniffiErr := rustCallWithError[SqliteError](FfiConverterSqliteError{}, func(_uniffiStatus *C.RustCallStatus) unsafe.Pointer {
		return C.uniffi_bdkffi_fn_constructor_connection_new(FfiConverterStringINSTANCE.Lower(path), _uniffiStatus)
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue *Connection
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterConnectionINSTANCE.Lift(_uniffiRV), nil
	}
}

func ConnectionNewInMemory() (*Connection, error) {
	_uniffiRV, _uniffiErr := rustCallWithError[SqliteError](FfiConverterSqliteError{}, func(_uniffiStatus *C.RustCallStatus) unsafe.Pointer {
		return C.uniffi_bdkffi_fn_constructor_connection_new_in_memory(_uniffiStatus)
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue *Connection
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterConnectionINSTANCE.Lift(_uniffiRV), nil
	}
}

func (object *Connection) Destroy() {
	runtime.SetFinalizer(object, nil)
	object.ffiObject.destroy()
}

type FfiConverterConnection struct{}

var FfiConverterConnectionINSTANCE = FfiConverterConnection{}

func (c FfiConverterConnection) Lift(pointer unsafe.Pointer) *Connection {
	result := &Connection{
		newFfiObject(
			pointer,
			func(pointer unsafe.Pointer, status *C.RustCallStatus) unsafe.Pointer {
				return C.uniffi_bdkffi_fn_clone_connection(pointer, status)
			},
			func(pointer unsafe.Pointer, status *C.RustCallStatus) {
				C.uniffi_bdkffi_fn_free_connection(pointer, status)
			},
		),
	}
	runtime.SetFinalizer(result, (*Connection).Destroy)
	return result
}

func (c FfiConverterConnection) Read(reader io.Reader) *Connection {
	return c.Lift(unsafe.Pointer(uintptr(readUint64(reader))))
}

func (c FfiConverterConnection) Lower(value *Connection) unsafe.Pointer {
	// TODO: this is bad - all synchronization from ObjectRuntime.go is discarded here,
	// because the pointer will be decremented immediately after this function returns,
	// and someone will be left holding onto a non-locked pointer.
	pointer := value.ffiObject.incrementPointer("*Connection")
	defer value.ffiObject.decrementPointer()
	return pointer

}

func (c FfiConverterConnection) Write(writer io.Writer, value *Connection) {
	writeUint64(writer, uint64(uintptr(c.Lower(value))))
}

type FfiDestroyerConnection struct{}

func (_ FfiDestroyerConnection) Destroy(value *Connection) {
	value.Destroy()
}

type DerivationPathInterface interface {
}
type DerivationPath struct {
	ffiObject FfiObject
}

func NewDerivationPath(path string) (*DerivationPath, error) {
	_uniffiRV, _uniffiErr := rustCallWithError[Bip32Error](FfiConverterBip32Error{}, func(_uniffiStatus *C.RustCallStatus) unsafe.Pointer {
		return C.uniffi_bdkffi_fn_constructor_derivationpath_new(FfiConverterStringINSTANCE.Lower(path), _uniffiStatus)
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue *DerivationPath
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterDerivationPathINSTANCE.Lift(_uniffiRV), nil
	}
}

func (object *DerivationPath) Destroy() {
	runtime.SetFinalizer(object, nil)
	object.ffiObject.destroy()
}

type FfiConverterDerivationPath struct{}

var FfiConverterDerivationPathINSTANCE = FfiConverterDerivationPath{}

func (c FfiConverterDerivationPath) Lift(pointer unsafe.Pointer) *DerivationPath {
	result := &DerivationPath{
		newFfiObject(
			pointer,
			func(pointer unsafe.Pointer, status *C.RustCallStatus) unsafe.Pointer {
				return C.uniffi_bdkffi_fn_clone_derivationpath(pointer, status)
			},
			func(pointer unsafe.Pointer, status *C.RustCallStatus) {
				C.uniffi_bdkffi_fn_free_derivationpath(pointer, status)
			},
		),
	}
	runtime.SetFinalizer(result, (*DerivationPath).Destroy)
	return result
}

func (c FfiConverterDerivationPath) Read(reader io.Reader) *DerivationPath {
	return c.Lift(unsafe.Pointer(uintptr(readUint64(reader))))
}

func (c FfiConverterDerivationPath) Lower(value *DerivationPath) unsafe.Pointer {
	// TODO: this is bad - all synchronization from ObjectRuntime.go is discarded here,
	// because the pointer will be decremented immediately after this function returns,
	// and someone will be left holding onto a non-locked pointer.
	pointer := value.ffiObject.incrementPointer("*DerivationPath")
	defer value.ffiObject.decrementPointer()
	return pointer

}

func (c FfiConverterDerivationPath) Write(writer io.Writer, value *DerivationPath) {
	writeUint64(writer, uint64(uintptr(c.Lower(value))))
}

type FfiDestroyerDerivationPath struct{}

func (_ FfiDestroyerDerivationPath) Destroy(value *DerivationPath) {
	value.Destroy()
}

type DescriptorInterface interface {
	// Whether or not this key has multiple derivation paths.
	IsMultipath() bool
	// Get as many descriptors as different paths in this descriptor.
	//
	// For multipath descriptors it will return as many descriptors as there is
	// "parallel" paths. For regular descriptors it will just return itself.
	ToSingleDescriptors() ([]*Descriptor, error)
	ToStringWithSecret() string
}
type Descriptor struct {
	ffiObject FfiObject
}

func NewDescriptor(descriptor string, network Network) (*Descriptor, error) {
	_uniffiRV, _uniffiErr := rustCallWithError[DescriptorError](FfiConverterDescriptorError{}, func(_uniffiStatus *C.RustCallStatus) unsafe.Pointer {
		return C.uniffi_bdkffi_fn_constructor_descriptor_new(FfiConverterStringINSTANCE.Lower(descriptor), FfiConverterNetworkINSTANCE.Lower(network), _uniffiStatus)
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue *Descriptor
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterDescriptorINSTANCE.Lift(_uniffiRV), nil
	}
}

func DescriptorNewBip44(secretKey *DescriptorSecretKey, keychain KeychainKind, network Network) *Descriptor {
	return FfiConverterDescriptorINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) unsafe.Pointer {
		return C.uniffi_bdkffi_fn_constructor_descriptor_new_bip44(FfiConverterDescriptorSecretKeyINSTANCE.Lower(secretKey), FfiConverterKeychainKindINSTANCE.Lower(keychain), FfiConverterNetworkINSTANCE.Lower(network), _uniffiStatus)
	}))
}

func DescriptorNewBip44Public(publicKey *DescriptorPublicKey, fingerprint string, keychain KeychainKind, network Network) *Descriptor {
	return FfiConverterDescriptorINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) unsafe.Pointer {
		return C.uniffi_bdkffi_fn_constructor_descriptor_new_bip44_public(FfiConverterDescriptorPublicKeyINSTANCE.Lower(publicKey), FfiConverterStringINSTANCE.Lower(fingerprint), FfiConverterKeychainKindINSTANCE.Lower(keychain), FfiConverterNetworkINSTANCE.Lower(network), _uniffiStatus)
	}))
}

func DescriptorNewBip49(secretKey *DescriptorSecretKey, keychain KeychainKind, network Network) *Descriptor {
	return FfiConverterDescriptorINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) unsafe.Pointer {
		return C.uniffi_bdkffi_fn_constructor_descriptor_new_bip49(FfiConverterDescriptorSecretKeyINSTANCE.Lower(secretKey), FfiConverterKeychainKindINSTANCE.Lower(keychain), FfiConverterNetworkINSTANCE.Lower(network), _uniffiStatus)
	}))
}

func DescriptorNewBip49Public(publicKey *DescriptorPublicKey, fingerprint string, keychain KeychainKind, network Network) *Descriptor {
	return FfiConverterDescriptorINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) unsafe.Pointer {
		return C.uniffi_bdkffi_fn_constructor_descriptor_new_bip49_public(FfiConverterDescriptorPublicKeyINSTANCE.Lower(publicKey), FfiConverterStringINSTANCE.Lower(fingerprint), FfiConverterKeychainKindINSTANCE.Lower(keychain), FfiConverterNetworkINSTANCE.Lower(network), _uniffiStatus)
	}))
}

func DescriptorNewBip84(secretKey *DescriptorSecretKey, keychain KeychainKind, network Network) *Descriptor {
	return FfiConverterDescriptorINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) unsafe.Pointer {
		return C.uniffi_bdkffi_fn_constructor_descriptor_new_bip84(FfiConverterDescriptorSecretKeyINSTANCE.Lower(secretKey), FfiConverterKeychainKindINSTANCE.Lower(keychain), FfiConverterNetworkINSTANCE.Lower(network), _uniffiStatus)
	}))
}

func DescriptorNewBip84Public(publicKey *DescriptorPublicKey, fingerprint string, keychain KeychainKind, network Network) *Descriptor {
	return FfiConverterDescriptorINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) unsafe.Pointer {
		return C.uniffi_bdkffi_fn_constructor_descriptor_new_bip84_public(FfiConverterDescriptorPublicKeyINSTANCE.Lower(publicKey), FfiConverterStringINSTANCE.Lower(fingerprint), FfiConverterKeychainKindINSTANCE.Lower(keychain), FfiConverterNetworkINSTANCE.Lower(network), _uniffiStatus)
	}))
}

func DescriptorNewBip86(secretKey *DescriptorSecretKey, keychain KeychainKind, network Network) *Descriptor {
	return FfiConverterDescriptorINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) unsafe.Pointer {
		return C.uniffi_bdkffi_fn_constructor_descriptor_new_bip86(FfiConverterDescriptorSecretKeyINSTANCE.Lower(secretKey), FfiConverterKeychainKindINSTANCE.Lower(keychain), FfiConverterNetworkINSTANCE.Lower(network), _uniffiStatus)
	}))
}

func DescriptorNewBip86Public(publicKey *DescriptorPublicKey, fingerprint string, keychain KeychainKind, network Network) *Descriptor {
	return FfiConverterDescriptorINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) unsafe.Pointer {
		return C.uniffi_bdkffi_fn_constructor_descriptor_new_bip86_public(FfiConverterDescriptorPublicKeyINSTANCE.Lower(publicKey), FfiConverterStringINSTANCE.Lower(fingerprint), FfiConverterKeychainKindINSTANCE.Lower(keychain), FfiConverterNetworkINSTANCE.Lower(network), _uniffiStatus)
	}))
}

// Whether or not this key has multiple derivation paths.
func (_self *Descriptor) IsMultipath() bool {
	_pointer := _self.ffiObject.incrementPointer("*Descriptor")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterBoolINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) C.int8_t {
		return C.uniffi_bdkffi_fn_method_descriptor_is_multipath(
			_pointer, _uniffiStatus)
	}))
}

// Get as many descriptors as different paths in this descriptor.
//
// For multipath descriptors it will return as many descriptors as there is
// "parallel" paths. For regular descriptors it will just return itself.
func (_self *Descriptor) ToSingleDescriptors() ([]*Descriptor, error) {
	_pointer := _self.ffiObject.incrementPointer("*Descriptor")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[MiniscriptError](FfiConverterMiniscriptError{}, func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_bdkffi_fn_method_descriptor_to_single_descriptors(
				_pointer, _uniffiStatus),
		}
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue []*Descriptor
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterSequenceDescriptorINSTANCE.Lift(_uniffiRV), nil
	}
}

func (_self *Descriptor) ToStringWithSecret() string {
	_pointer := _self.ffiObject.incrementPointer("*Descriptor")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterStringINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_bdkffi_fn_method_descriptor_to_string_with_secret(
				_pointer, _uniffiStatus),
		}
	}))
}

func (_self *Descriptor) String() string {
	_pointer := _self.ffiObject.incrementPointer("*Descriptor")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterStringINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_bdkffi_fn_method_descriptor_uniffi_trait_display(
				_pointer, _uniffiStatus),
		}
	}))
}

func (object *Descriptor) Destroy() {
	runtime.SetFinalizer(object, nil)
	object.ffiObject.destroy()
}

type FfiConverterDescriptor struct{}

var FfiConverterDescriptorINSTANCE = FfiConverterDescriptor{}

func (c FfiConverterDescriptor) Lift(pointer unsafe.Pointer) *Descriptor {
	result := &Descriptor{
		newFfiObject(
			pointer,
			func(pointer unsafe.Pointer, status *C.RustCallStatus) unsafe.Pointer {
				return C.uniffi_bdkffi_fn_clone_descriptor(pointer, status)
			},
			func(pointer unsafe.Pointer, status *C.RustCallStatus) {
				C.uniffi_bdkffi_fn_free_descriptor(pointer, status)
			},
		),
	}
	runtime.SetFinalizer(result, (*Descriptor).Destroy)
	return result
}

func (c FfiConverterDescriptor) Read(reader io.Reader) *Descriptor {
	return c.Lift(unsafe.Pointer(uintptr(readUint64(reader))))
}

func (c FfiConverterDescriptor) Lower(value *Descriptor) unsafe.Pointer {
	// TODO: this is bad - all synchronization from ObjectRuntime.go is discarded here,
	// because the pointer will be decremented immediately after this function returns,
	// and someone will be left holding onto a non-locked pointer.
	pointer := value.ffiObject.incrementPointer("*Descriptor")
	defer value.ffiObject.decrementPointer()
	return pointer

}

func (c FfiConverterDescriptor) Write(writer io.Writer, value *Descriptor) {
	writeUint64(writer, uint64(uintptr(c.Lower(value))))
}

type FfiDestroyerDescriptor struct{}

func (_ FfiDestroyerDescriptor) Destroy(value *Descriptor) {
	value.Destroy()
}

type DescriptorPublicKeyInterface interface {
	AsString() string
	Derive(path *DerivationPath) (*DescriptorPublicKey, error)
	Extend(path *DerivationPath) (*DescriptorPublicKey, error)
	// Whether or not this key has multiple derivation paths.
	IsMultipath() bool
	// The fingerprint of the master key associated with this key, `0x00000000` if none.
	MasterFingerprint() string
}
type DescriptorPublicKey struct {
	ffiObject FfiObject
}

func DescriptorPublicKeyFromString(publicKey string) (*DescriptorPublicKey, error) {
	_uniffiRV, _uniffiErr := rustCallWithError[DescriptorKeyError](FfiConverterDescriptorKeyError{}, func(_uniffiStatus *C.RustCallStatus) unsafe.Pointer {
		return C.uniffi_bdkffi_fn_constructor_descriptorpublickey_from_string(FfiConverterStringINSTANCE.Lower(publicKey), _uniffiStatus)
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue *DescriptorPublicKey
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterDescriptorPublicKeyINSTANCE.Lift(_uniffiRV), nil
	}
}

func (_self *DescriptorPublicKey) AsString() string {
	_pointer := _self.ffiObject.incrementPointer("*DescriptorPublicKey")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterStringINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_bdkffi_fn_method_descriptorpublickey_as_string(
				_pointer, _uniffiStatus),
		}
	}))
}

func (_self *DescriptorPublicKey) Derive(path *DerivationPath) (*DescriptorPublicKey, error) {
	_pointer := _self.ffiObject.incrementPointer("*DescriptorPublicKey")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[DescriptorKeyError](FfiConverterDescriptorKeyError{}, func(_uniffiStatus *C.RustCallStatus) unsafe.Pointer {
		return C.uniffi_bdkffi_fn_method_descriptorpublickey_derive(
			_pointer, FfiConverterDerivationPathINSTANCE.Lower(path), _uniffiStatus)
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue *DescriptorPublicKey
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterDescriptorPublicKeyINSTANCE.Lift(_uniffiRV), nil
	}
}

func (_self *DescriptorPublicKey) Extend(path *DerivationPath) (*DescriptorPublicKey, error) {
	_pointer := _self.ffiObject.incrementPointer("*DescriptorPublicKey")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[DescriptorKeyError](FfiConverterDescriptorKeyError{}, func(_uniffiStatus *C.RustCallStatus) unsafe.Pointer {
		return C.uniffi_bdkffi_fn_method_descriptorpublickey_extend(
			_pointer, FfiConverterDerivationPathINSTANCE.Lower(path), _uniffiStatus)
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue *DescriptorPublicKey
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterDescriptorPublicKeyINSTANCE.Lift(_uniffiRV), nil
	}
}

// Whether or not this key has multiple derivation paths.
func (_self *DescriptorPublicKey) IsMultipath() bool {
	_pointer := _self.ffiObject.incrementPointer("*DescriptorPublicKey")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterBoolINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) C.int8_t {
		return C.uniffi_bdkffi_fn_method_descriptorpublickey_is_multipath(
			_pointer, _uniffiStatus)
	}))
}

// The fingerprint of the master key associated with this key, `0x00000000` if none.
func (_self *DescriptorPublicKey) MasterFingerprint() string {
	_pointer := _self.ffiObject.incrementPointer("*DescriptorPublicKey")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterStringINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_bdkffi_fn_method_descriptorpublickey_master_fingerprint(
				_pointer, _uniffiStatus),
		}
	}))
}
func (object *DescriptorPublicKey) Destroy() {
	runtime.SetFinalizer(object, nil)
	object.ffiObject.destroy()
}

type FfiConverterDescriptorPublicKey struct{}

var FfiConverterDescriptorPublicKeyINSTANCE = FfiConverterDescriptorPublicKey{}

func (c FfiConverterDescriptorPublicKey) Lift(pointer unsafe.Pointer) *DescriptorPublicKey {
	result := &DescriptorPublicKey{
		newFfiObject(
			pointer,
			func(pointer unsafe.Pointer, status *C.RustCallStatus) unsafe.Pointer {
				return C.uniffi_bdkffi_fn_clone_descriptorpublickey(pointer, status)
			},
			func(pointer unsafe.Pointer, status *C.RustCallStatus) {
				C.uniffi_bdkffi_fn_free_descriptorpublickey(pointer, status)
			},
		),
	}
	runtime.SetFinalizer(result, (*DescriptorPublicKey).Destroy)
	return result
}

func (c FfiConverterDescriptorPublicKey) Read(reader io.Reader) *DescriptorPublicKey {
	return c.Lift(unsafe.Pointer(uintptr(readUint64(reader))))
}

func (c FfiConverterDescriptorPublicKey) Lower(value *DescriptorPublicKey) unsafe.Pointer {
	// TODO: this is bad - all synchronization from ObjectRuntime.go is discarded here,
	// because the pointer will be decremented immediately after this function returns,
	// and someone will be left holding onto a non-locked pointer.
	pointer := value.ffiObject.incrementPointer("*DescriptorPublicKey")
	defer value.ffiObject.decrementPointer()
	return pointer

}

func (c FfiConverterDescriptorPublicKey) Write(writer io.Writer, value *DescriptorPublicKey) {
	writeUint64(writer, uint64(uintptr(c.Lower(value))))
}

type FfiDestroyerDescriptorPublicKey struct{}

func (_ FfiDestroyerDescriptorPublicKey) Destroy(value *DescriptorPublicKey) {
	value.Destroy()
}

type DescriptorSecretKeyInterface interface {
	AsPublic() *DescriptorPublicKey
	AsString() string
	Derive(path *DerivationPath) (*DescriptorSecretKey, error)
	Extend(path *DerivationPath) (*DescriptorSecretKey, error)
	SecretBytes() []uint8
}
type DescriptorSecretKey struct {
	ffiObject FfiObject
}

func NewDescriptorSecretKey(network Network, mnemonic *Mnemonic, password *string) *DescriptorSecretKey {
	return FfiConverterDescriptorSecretKeyINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) unsafe.Pointer {
		return C.uniffi_bdkffi_fn_constructor_descriptorsecretkey_new(FfiConverterNetworkINSTANCE.Lower(network), FfiConverterMnemonicINSTANCE.Lower(mnemonic), FfiConverterOptionalStringINSTANCE.Lower(password), _uniffiStatus)
	}))
}

func DescriptorSecretKeyFromString(secretKey string) (*DescriptorSecretKey, error) {
	_uniffiRV, _uniffiErr := rustCallWithError[DescriptorKeyError](FfiConverterDescriptorKeyError{}, func(_uniffiStatus *C.RustCallStatus) unsafe.Pointer {
		return C.uniffi_bdkffi_fn_constructor_descriptorsecretkey_from_string(FfiConverterStringINSTANCE.Lower(secretKey), _uniffiStatus)
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue *DescriptorSecretKey
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterDescriptorSecretKeyINSTANCE.Lift(_uniffiRV), nil
	}
}

func (_self *DescriptorSecretKey) AsPublic() *DescriptorPublicKey {
	_pointer := _self.ffiObject.incrementPointer("*DescriptorSecretKey")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterDescriptorPublicKeyINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) unsafe.Pointer {
		return C.uniffi_bdkffi_fn_method_descriptorsecretkey_as_public(
			_pointer, _uniffiStatus)
	}))
}

func (_self *DescriptorSecretKey) AsString() string {
	_pointer := _self.ffiObject.incrementPointer("*DescriptorSecretKey")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterStringINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_bdkffi_fn_method_descriptorsecretkey_as_string(
				_pointer, _uniffiStatus),
		}
	}))
}

func (_self *DescriptorSecretKey) Derive(path *DerivationPath) (*DescriptorSecretKey, error) {
	_pointer := _self.ffiObject.incrementPointer("*DescriptorSecretKey")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[DescriptorKeyError](FfiConverterDescriptorKeyError{}, func(_uniffiStatus *C.RustCallStatus) unsafe.Pointer {
		return C.uniffi_bdkffi_fn_method_descriptorsecretkey_derive(
			_pointer, FfiConverterDerivationPathINSTANCE.Lower(path), _uniffiStatus)
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue *DescriptorSecretKey
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterDescriptorSecretKeyINSTANCE.Lift(_uniffiRV), nil
	}
}

func (_self *DescriptorSecretKey) Extend(path *DerivationPath) (*DescriptorSecretKey, error) {
	_pointer := _self.ffiObject.incrementPointer("*DescriptorSecretKey")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[DescriptorKeyError](FfiConverterDescriptorKeyError{}, func(_uniffiStatus *C.RustCallStatus) unsafe.Pointer {
		return C.uniffi_bdkffi_fn_method_descriptorsecretkey_extend(
			_pointer, FfiConverterDerivationPathINSTANCE.Lower(path), _uniffiStatus)
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue *DescriptorSecretKey
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterDescriptorSecretKeyINSTANCE.Lift(_uniffiRV), nil
	}
}

func (_self *DescriptorSecretKey) SecretBytes() []uint8 {
	_pointer := _self.ffiObject.incrementPointer("*DescriptorSecretKey")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterSequenceUint8INSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_bdkffi_fn_method_descriptorsecretkey_secret_bytes(
				_pointer, _uniffiStatus),
		}
	}))
}
func (object *DescriptorSecretKey) Destroy() {
	runtime.SetFinalizer(object, nil)
	object.ffiObject.destroy()
}

type FfiConverterDescriptorSecretKey struct{}

var FfiConverterDescriptorSecretKeyINSTANCE = FfiConverterDescriptorSecretKey{}

func (c FfiConverterDescriptorSecretKey) Lift(pointer unsafe.Pointer) *DescriptorSecretKey {
	result := &DescriptorSecretKey{
		newFfiObject(
			pointer,
			func(pointer unsafe.Pointer, status *C.RustCallStatus) unsafe.Pointer {
				return C.uniffi_bdkffi_fn_clone_descriptorsecretkey(pointer, status)
			},
			func(pointer unsafe.Pointer, status *C.RustCallStatus) {
				C.uniffi_bdkffi_fn_free_descriptorsecretkey(pointer, status)
			},
		),
	}
	runtime.SetFinalizer(result, (*DescriptorSecretKey).Destroy)
	return result
}

func (c FfiConverterDescriptorSecretKey) Read(reader io.Reader) *DescriptorSecretKey {
	return c.Lift(unsafe.Pointer(uintptr(readUint64(reader))))
}

func (c FfiConverterDescriptorSecretKey) Lower(value *DescriptorSecretKey) unsafe.Pointer {
	// TODO: this is bad - all synchronization from ObjectRuntime.go is discarded here,
	// because the pointer will be decremented immediately after this function returns,
	// and someone will be left holding onto a non-locked pointer.
	pointer := value.ffiObject.incrementPointer("*DescriptorSecretKey")
	defer value.ffiObject.decrementPointer()
	return pointer

}

func (c FfiConverterDescriptorSecretKey) Write(writer io.Writer, value *DescriptorSecretKey) {
	writeUint64(writer, uint64(uintptr(c.Lower(value))))
}

type FfiDestroyerDescriptorSecretKey struct{}

func (_ FfiDestroyerDescriptorSecretKey) Destroy(value *DescriptorSecretKey) {
	value.Destroy()
}

// Wrapper around an electrum_client::ElectrumApi which includes an internal in-memory transaction
// cache to avoid re-fetching already downloaded transactions.
type ElectrumClientInterface interface {
	// Estimates the fee required in bitcoin per kilobyte to confirm a transaction in `number` blocks.
	EstimateFee(number uint64) (float64, error)
	// Full scan the keychain scripts specified with the blockchain (via an Electrum client) and
	// returns updates for bdk_chain data structures.
	//
	// - `request`: struct with data required to perform a spk-based blockchain client
	//   full scan, see `FullScanRequest`.
	// - `stop_gap`: the full scan for each keychain stops after a gap of script pubkeys with no
	//   associated transactions.
	// - `batch_size`: specifies the max number of script pubkeys to request for in a single batch
	//   request.
	// - `fetch_prev_txouts`: specifies whether we want previous `TxOuts` for fee calculation. Note
	//   that this requires additional calls to the Electrum server, but is necessary for
	//   calculating the fee on a transaction if your wallet does not own the inputs. Methods like
	//   `Wallet.calculate_fee` and `Wallet.calculate_fee_rate` will return a
	//   `CalculateFeeError::MissingTxOut` error if those TxOuts are not present in the transaction
	//   graph.
	FullScan(request *FullScanRequest, stopGap uint64, batchSize uint64, fetchPrevTxouts bool) (*Update, error)
	// Returns the capabilities of the server.
	ServerFeatures() (ServerFeaturesRes, error)
	// Sync a set of scripts with the blockchain (via an Electrum client) for the data specified and returns updates for bdk_chain data structures.
	//
	// - `request`: struct with data required to perform a spk-based blockchain client
	//   sync, see `SyncRequest`.
	// - `batch_size`: specifies the max number of script pubkeys to request for in a single batch
	//   request.
	// - `fetch_prev_txouts`: specifies whether we want previous `TxOuts` for fee calculation. Note
	//   that this requires additional calls to the Electrum server, but is necessary for
	//   calculating the fee on a transaction if your wallet does not own the inputs. Methods like
	//   `Wallet.calculate_fee` and `Wallet.calculate_fee_rate` will return a
	//   `CalculateFeeError::MissingTxOut` error if those TxOuts are not present in the transaction
	//   graph.
	//
	// If the scripts to sync are unknown, such as when restoring or importing a keychain that may
	// include scripts that have been used, use full_scan with the keychain.
	Sync(request *SyncRequest, batchSize uint64, fetchPrevTxouts bool) (*Update, error)
	// Broadcasts a transaction to the network.
	TransactionBroadcast(tx *Transaction) (string, error)
}

// Wrapper around an electrum_client::ElectrumApi which includes an internal in-memory transaction
// cache to avoid re-fetching already downloaded transactions.
type ElectrumClient struct {
	ffiObject FfiObject
}

// Creates a new bdk client from a electrum_client::ElectrumApi
func NewElectrumClient(url string) (*ElectrumClient, error) {
	_uniffiRV, _uniffiErr := rustCallWithError[ElectrumError](FfiConverterElectrumError{}, func(_uniffiStatus *C.RustCallStatus) unsafe.Pointer {
		return C.uniffi_bdkffi_fn_constructor_electrumclient_new(FfiConverterStringINSTANCE.Lower(url), _uniffiStatus)
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue *ElectrumClient
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterElectrumClientINSTANCE.Lift(_uniffiRV), nil
	}
}

// Estimates the fee required in bitcoin per kilobyte to confirm a transaction in `number` blocks.
func (_self *ElectrumClient) EstimateFee(number uint64) (float64, error) {
	_pointer := _self.ffiObject.incrementPointer("*ElectrumClient")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[ElectrumError](FfiConverterElectrumError{}, func(_uniffiStatus *C.RustCallStatus) C.double {
		return C.uniffi_bdkffi_fn_method_electrumclient_estimate_fee(
			_pointer, FfiConverterUint64INSTANCE.Lower(number), _uniffiStatus)
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue float64
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterFloat64INSTANCE.Lift(_uniffiRV), nil
	}
}

// Full scan the keychain scripts specified with the blockchain (via an Electrum client) and
// returns updates for bdk_chain data structures.
//
//   - `request`: struct with data required to perform a spk-based blockchain client
//     full scan, see `FullScanRequest`.
//   - `stop_gap`: the full scan for each keychain stops after a gap of script pubkeys with no
//     associated transactions.
//   - `batch_size`: specifies the max number of script pubkeys to request for in a single batch
//     request.
//   - `fetch_prev_txouts`: specifies whether we want previous `TxOuts` for fee calculation. Note
//     that this requires additional calls to the Electrum server, but is necessary for
//     calculating the fee on a transaction if your wallet does not own the inputs. Methods like
//     `Wallet.calculate_fee` and `Wallet.calculate_fee_rate` will return a
//     `CalculateFeeError::MissingTxOut` error if those TxOuts are not present in the transaction
//     graph.
func (_self *ElectrumClient) FullScan(request *FullScanRequest, stopGap uint64, batchSize uint64, fetchPrevTxouts bool) (*Update, error) {
	_pointer := _self.ffiObject.incrementPointer("*ElectrumClient")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[ElectrumError](FfiConverterElectrumError{}, func(_uniffiStatus *C.RustCallStatus) unsafe.Pointer {
		return C.uniffi_bdkffi_fn_method_electrumclient_full_scan(
			_pointer, FfiConverterFullScanRequestINSTANCE.Lower(request), FfiConverterUint64INSTANCE.Lower(stopGap), FfiConverterUint64INSTANCE.Lower(batchSize), FfiConverterBoolINSTANCE.Lower(fetchPrevTxouts), _uniffiStatus)
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue *Update
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterUpdateINSTANCE.Lift(_uniffiRV), nil
	}
}

// Returns the capabilities of the server.
func (_self *ElectrumClient) ServerFeatures() (ServerFeaturesRes, error) {
	_pointer := _self.ffiObject.incrementPointer("*ElectrumClient")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[ElectrumError](FfiConverterElectrumError{}, func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_bdkffi_fn_method_electrumclient_server_features(
				_pointer, _uniffiStatus),
		}
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue ServerFeaturesRes
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterServerFeaturesResINSTANCE.Lift(_uniffiRV), nil
	}
}

// Sync a set of scripts with the blockchain (via an Electrum client) for the data specified and returns updates for bdk_chain data structures.
//
//   - `request`: struct with data required to perform a spk-based blockchain client
//     sync, see `SyncRequest`.
//   - `batch_size`: specifies the max number of script pubkeys to request for in a single batch
//     request.
//   - `fetch_prev_txouts`: specifies whether we want previous `TxOuts` for fee calculation. Note
//     that this requires additional calls to the Electrum server, but is necessary for
//     calculating the fee on a transaction if your wallet does not own the inputs. Methods like
//     `Wallet.calculate_fee` and `Wallet.calculate_fee_rate` will return a
//     `CalculateFeeError::MissingTxOut` error if those TxOuts are not present in the transaction
//     graph.
//
// If the scripts to sync are unknown, such as when restoring or importing a keychain that may
// include scripts that have been used, use full_scan with the keychain.
func (_self *ElectrumClient) Sync(request *SyncRequest, batchSize uint64, fetchPrevTxouts bool) (*Update, error) {
	_pointer := _self.ffiObject.incrementPointer("*ElectrumClient")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[ElectrumError](FfiConverterElectrumError{}, func(_uniffiStatus *C.RustCallStatus) unsafe.Pointer {
		return C.uniffi_bdkffi_fn_method_electrumclient_sync(
			_pointer, FfiConverterSyncRequestINSTANCE.Lower(request), FfiConverterUint64INSTANCE.Lower(batchSize), FfiConverterBoolINSTANCE.Lower(fetchPrevTxouts), _uniffiStatus)
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue *Update
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterUpdateINSTANCE.Lift(_uniffiRV), nil
	}
}

// Broadcasts a transaction to the network.
func (_self *ElectrumClient) TransactionBroadcast(tx *Transaction) (string, error) {
	_pointer := _self.ffiObject.incrementPointer("*ElectrumClient")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[ElectrumError](FfiConverterElectrumError{}, func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_bdkffi_fn_method_electrumclient_transaction_broadcast(
				_pointer, FfiConverterTransactionINSTANCE.Lower(tx), _uniffiStatus),
		}
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue string
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterStringINSTANCE.Lift(_uniffiRV), nil
	}
}
func (object *ElectrumClient) Destroy() {
	runtime.SetFinalizer(object, nil)
	object.ffiObject.destroy()
}

type FfiConverterElectrumClient struct{}

var FfiConverterElectrumClientINSTANCE = FfiConverterElectrumClient{}

func (c FfiConverterElectrumClient) Lift(pointer unsafe.Pointer) *ElectrumClient {
	result := &ElectrumClient{
		newFfiObject(
			pointer,
			func(pointer unsafe.Pointer, status *C.RustCallStatus) unsafe.Pointer {
				return C.uniffi_bdkffi_fn_clone_electrumclient(pointer, status)
			},
			func(pointer unsafe.Pointer, status *C.RustCallStatus) {
				C.uniffi_bdkffi_fn_free_electrumclient(pointer, status)
			},
		),
	}
	runtime.SetFinalizer(result, (*ElectrumClient).Destroy)
	return result
}

func (c FfiConverterElectrumClient) Read(reader io.Reader) *ElectrumClient {
	return c.Lift(unsafe.Pointer(uintptr(readUint64(reader))))
}

func (c FfiConverterElectrumClient) Lower(value *ElectrumClient) unsafe.Pointer {
	// TODO: this is bad - all synchronization from ObjectRuntime.go is discarded here,
	// because the pointer will be decremented immediately after this function returns,
	// and someone will be left holding onto a non-locked pointer.
	pointer := value.ffiObject.incrementPointer("*ElectrumClient")
	defer value.ffiObject.decrementPointer()
	return pointer

}

func (c FfiConverterElectrumClient) Write(writer io.Writer, value *ElectrumClient) {
	writeUint64(writer, uint64(uintptr(c.Lower(value))))
}

type FfiDestroyerElectrumClient struct{}

func (_ FfiDestroyerElectrumClient) Destroy(value *ElectrumClient) {
	value.Destroy()
}

// Wrapper around an esplora_client::BlockingClient which includes an internal in-memory transaction
// cache to avoid re-fetching already downloaded transactions.
type EsploraClientInterface interface {
	// Broadcast a [`Transaction`] to Esplora.
	Broadcast(transaction *Transaction) error
	// Scan keychain scripts for transactions against Esplora, returning an update that can be
	// applied to the receiving structures.
	//
	// `request` provides the data required to perform a script-pubkey-based full scan
	// (see [`FullScanRequest`]). The full scan for each keychain (`K`) stops after a gap of
	// `stop_gap` script pubkeys with no associated transactions. `parallel_requests` specifies
	// the maximum number of HTTP requests to make in parallel.
	FullScan(request *FullScanRequest, stopGap uint64, parallelRequests uint64) (*Update, error)
	// Get the [`BlockHash`] of a specific block height
	GetBlockHash(blockHeight uint32) (string, error)
	// Get a map where the key is the confirmation target (in number of
	// blocks) and the value is the estimated feerate (in sat/vB).
	GetFeeEstimates() (map[uint16]float64, error)
	// Get the height of the current blockchain tip.
	GetHeight() (uint32, error)
	// Get a [`Transaction`] option given its [`Txid`].
	GetTx(txid string) (**Transaction, error)
	// Get transaction info given it's [`Txid`].
	GetTxInfo(txid string) (*Tx, error)
	// Get the status of a [`Transaction`] given its [`Txid`].
	GetTxStatus(txid string) (TxStatus, error)
	// Sync a set of scripts, txids, and/or outpoints against Esplora.
	//
	// `request` provides the data required to perform a script-pubkey-based sync (see
	// [`SyncRequest`]). `parallel_requests` specifies the maximum number of HTTP requests to make
	// in parallel.
	Sync(request *SyncRequest, parallelRequests uint64) (*Update, error)
}

// Wrapper around an esplora_client::BlockingClient which includes an internal in-memory transaction
// cache to avoid re-fetching already downloaded transactions.
type EsploraClient struct {
	ffiObject FfiObject
}

// Creates a new bdk client from a esplora_client::BlockingClient
func NewEsploraClient(url string) *EsploraClient {
	return FfiConverterEsploraClientINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) unsafe.Pointer {
		return C.uniffi_bdkffi_fn_constructor_esploraclient_new(FfiConverterStringINSTANCE.Lower(url), _uniffiStatus)
	}))
}

// Broadcast a [`Transaction`] to Esplora.
func (_self *EsploraClient) Broadcast(transaction *Transaction) error {
	_pointer := _self.ffiObject.incrementPointer("*EsploraClient")
	defer _self.ffiObject.decrementPointer()
	_, _uniffiErr := rustCallWithError[EsploraError](FfiConverterEsploraError{}, func(_uniffiStatus *C.RustCallStatus) bool {
		C.uniffi_bdkffi_fn_method_esploraclient_broadcast(
			_pointer, FfiConverterTransactionINSTANCE.Lower(transaction), _uniffiStatus)
		return false
	})
	return _uniffiErr.AsError()
}

// Scan keychain scripts for transactions against Esplora, returning an update that can be
// applied to the receiving structures.
//
// `request` provides the data required to perform a script-pubkey-based full scan
// (see [`FullScanRequest`]). The full scan for each keychain (`K`) stops after a gap of
// `stop_gap` script pubkeys with no associated transactions. `parallel_requests` specifies
// the maximum number of HTTP requests to make in parallel.
func (_self *EsploraClient) FullScan(request *FullScanRequest, stopGap uint64, parallelRequests uint64) (*Update, error) {
	_pointer := _self.ffiObject.incrementPointer("*EsploraClient")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[EsploraError](FfiConverterEsploraError{}, func(_uniffiStatus *C.RustCallStatus) unsafe.Pointer {
		return C.uniffi_bdkffi_fn_method_esploraclient_full_scan(
			_pointer, FfiConverterFullScanRequestINSTANCE.Lower(request), FfiConverterUint64INSTANCE.Lower(stopGap), FfiConverterUint64INSTANCE.Lower(parallelRequests), _uniffiStatus)
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue *Update
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterUpdateINSTANCE.Lift(_uniffiRV), nil
	}
}

// Get the [`BlockHash`] of a specific block height
func (_self *EsploraClient) GetBlockHash(blockHeight uint32) (string, error) {
	_pointer := _self.ffiObject.incrementPointer("*EsploraClient")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[EsploraError](FfiConverterEsploraError{}, func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_bdkffi_fn_method_esploraclient_get_block_hash(
				_pointer, FfiConverterUint32INSTANCE.Lower(blockHeight), _uniffiStatus),
		}
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue string
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterStringINSTANCE.Lift(_uniffiRV), nil
	}
}

// Get a map where the key is the confirmation target (in number of
// blocks) and the value is the estimated feerate (in sat/vB).
func (_self *EsploraClient) GetFeeEstimates() (map[uint16]float64, error) {
	_pointer := _self.ffiObject.incrementPointer("*EsploraClient")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[EsploraError](FfiConverterEsploraError{}, func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_bdkffi_fn_method_esploraclient_get_fee_estimates(
				_pointer, _uniffiStatus),
		}
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue map[uint16]float64
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterMapUint16Float64INSTANCE.Lift(_uniffiRV), nil
	}
}

// Get the height of the current blockchain tip.
func (_self *EsploraClient) GetHeight() (uint32, error) {
	_pointer := _self.ffiObject.incrementPointer("*EsploraClient")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[EsploraError](FfiConverterEsploraError{}, func(_uniffiStatus *C.RustCallStatus) C.uint32_t {
		return C.uniffi_bdkffi_fn_method_esploraclient_get_height(
			_pointer, _uniffiStatus)
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue uint32
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterUint32INSTANCE.Lift(_uniffiRV), nil
	}
}

// Get a [`Transaction`] option given its [`Txid`].
func (_self *EsploraClient) GetTx(txid string) (**Transaction, error) {
	_pointer := _self.ffiObject.incrementPointer("*EsploraClient")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[EsploraError](FfiConverterEsploraError{}, func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_bdkffi_fn_method_esploraclient_get_tx(
				_pointer, FfiConverterStringINSTANCE.Lower(txid), _uniffiStatus),
		}
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue **Transaction
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterOptionalTransactionINSTANCE.Lift(_uniffiRV), nil
	}
}

// Get transaction info given it's [`Txid`].
func (_self *EsploraClient) GetTxInfo(txid string) (*Tx, error) {
	_pointer := _self.ffiObject.incrementPointer("*EsploraClient")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[EsploraError](FfiConverterEsploraError{}, func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_bdkffi_fn_method_esploraclient_get_tx_info(
				_pointer, FfiConverterStringINSTANCE.Lower(txid), _uniffiStatus),
		}
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue *Tx
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterOptionalTxINSTANCE.Lift(_uniffiRV), nil
	}
}

// Get the status of a [`Transaction`] given its [`Txid`].
func (_self *EsploraClient) GetTxStatus(txid string) (TxStatus, error) {
	_pointer := _self.ffiObject.incrementPointer("*EsploraClient")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[EsploraError](FfiConverterEsploraError{}, func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_bdkffi_fn_method_esploraclient_get_tx_status(
				_pointer, FfiConverterStringINSTANCE.Lower(txid), _uniffiStatus),
		}
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue TxStatus
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterTxStatusINSTANCE.Lift(_uniffiRV), nil
	}
}

// Sync a set of scripts, txids, and/or outpoints against Esplora.
//
// `request` provides the data required to perform a script-pubkey-based sync (see
// [`SyncRequest`]). `parallel_requests` specifies the maximum number of HTTP requests to make
// in parallel.
func (_self *EsploraClient) Sync(request *SyncRequest, parallelRequests uint64) (*Update, error) {
	_pointer := _self.ffiObject.incrementPointer("*EsploraClient")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[EsploraError](FfiConverterEsploraError{}, func(_uniffiStatus *C.RustCallStatus) unsafe.Pointer {
		return C.uniffi_bdkffi_fn_method_esploraclient_sync(
			_pointer, FfiConverterSyncRequestINSTANCE.Lower(request), FfiConverterUint64INSTANCE.Lower(parallelRequests), _uniffiStatus)
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue *Update
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterUpdateINSTANCE.Lift(_uniffiRV), nil
	}
}
func (object *EsploraClient) Destroy() {
	runtime.SetFinalizer(object, nil)
	object.ffiObject.destroy()
}

type FfiConverterEsploraClient struct{}

var FfiConverterEsploraClientINSTANCE = FfiConverterEsploraClient{}

func (c FfiConverterEsploraClient) Lift(pointer unsafe.Pointer) *EsploraClient {
	result := &EsploraClient{
		newFfiObject(
			pointer,
			func(pointer unsafe.Pointer, status *C.RustCallStatus) unsafe.Pointer {
				return C.uniffi_bdkffi_fn_clone_esploraclient(pointer, status)
			},
			func(pointer unsafe.Pointer, status *C.RustCallStatus) {
				C.uniffi_bdkffi_fn_free_esploraclient(pointer, status)
			},
		),
	}
	runtime.SetFinalizer(result, (*EsploraClient).Destroy)
	return result
}

func (c FfiConverterEsploraClient) Read(reader io.Reader) *EsploraClient {
	return c.Lift(unsafe.Pointer(uintptr(readUint64(reader))))
}

func (c FfiConverterEsploraClient) Lower(value *EsploraClient) unsafe.Pointer {
	// TODO: this is bad - all synchronization from ObjectRuntime.go is discarded here,
	// because the pointer will be decremented immediately after this function returns,
	// and someone will be left holding onto a non-locked pointer.
	pointer := value.ffiObject.incrementPointer("*EsploraClient")
	defer value.ffiObject.decrementPointer()
	return pointer

}

func (c FfiConverterEsploraClient) Write(writer io.Writer, value *EsploraClient) {
	writeUint64(writer, uint64(uintptr(c.Lower(value))))
}

type FfiDestroyerEsploraClient struct{}

func (_ FfiDestroyerEsploraClient) Destroy(value *EsploraClient) {
	value.Destroy()
}

type FeeRateInterface interface {
	ToSatPerKwu() uint64
	ToSatPerVbCeil() uint64
	ToSatPerVbFloor() uint64
}
type FeeRate struct {
	ffiObject FfiObject
}

func FeeRateFromSatPerKwu(satPerKwu uint64) *FeeRate {
	return FfiConverterFeeRateINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) unsafe.Pointer {
		return C.uniffi_bdkffi_fn_constructor_feerate_from_sat_per_kwu(FfiConverterUint64INSTANCE.Lower(satPerKwu), _uniffiStatus)
	}))
}

func FeeRateFromSatPerVb(satPerVb uint64) (*FeeRate, error) {
	_uniffiRV, _uniffiErr := rustCallWithError[FeeRateError](FfiConverterFeeRateError{}, func(_uniffiStatus *C.RustCallStatus) unsafe.Pointer {
		return C.uniffi_bdkffi_fn_constructor_feerate_from_sat_per_vb(FfiConverterUint64INSTANCE.Lower(satPerVb), _uniffiStatus)
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue *FeeRate
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterFeeRateINSTANCE.Lift(_uniffiRV), nil
	}
}

func (_self *FeeRate) ToSatPerKwu() uint64 {
	_pointer := _self.ffiObject.incrementPointer("*FeeRate")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterUint64INSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint64_t {
		return C.uniffi_bdkffi_fn_method_feerate_to_sat_per_kwu(
			_pointer, _uniffiStatus)
	}))
}

func (_self *FeeRate) ToSatPerVbCeil() uint64 {
	_pointer := _self.ffiObject.incrementPointer("*FeeRate")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterUint64INSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint64_t {
		return C.uniffi_bdkffi_fn_method_feerate_to_sat_per_vb_ceil(
			_pointer, _uniffiStatus)
	}))
}

func (_self *FeeRate) ToSatPerVbFloor() uint64 {
	_pointer := _self.ffiObject.incrementPointer("*FeeRate")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterUint64INSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint64_t {
		return C.uniffi_bdkffi_fn_method_feerate_to_sat_per_vb_floor(
			_pointer, _uniffiStatus)
	}))
}
func (object *FeeRate) Destroy() {
	runtime.SetFinalizer(object, nil)
	object.ffiObject.destroy()
}

type FfiConverterFeeRate struct{}

var FfiConverterFeeRateINSTANCE = FfiConverterFeeRate{}

func (c FfiConverterFeeRate) Lift(pointer unsafe.Pointer) *FeeRate {
	result := &FeeRate{
		newFfiObject(
			pointer,
			func(pointer unsafe.Pointer, status *C.RustCallStatus) unsafe.Pointer {
				return C.uniffi_bdkffi_fn_clone_feerate(pointer, status)
			},
			func(pointer unsafe.Pointer, status *C.RustCallStatus) {
				C.uniffi_bdkffi_fn_free_feerate(pointer, status)
			},
		),
	}
	runtime.SetFinalizer(result, (*FeeRate).Destroy)
	return result
}

func (c FfiConverterFeeRate) Read(reader io.Reader) *FeeRate {
	return c.Lift(unsafe.Pointer(uintptr(readUint64(reader))))
}

func (c FfiConverterFeeRate) Lower(value *FeeRate) unsafe.Pointer {
	// TODO: this is bad - all synchronization from ObjectRuntime.go is discarded here,
	// because the pointer will be decremented immediately after this function returns,
	// and someone will be left holding onto a non-locked pointer.
	pointer := value.ffiObject.incrementPointer("*FeeRate")
	defer value.ffiObject.decrementPointer()
	return pointer

}

func (c FfiConverterFeeRate) Write(writer io.Writer, value *FeeRate) {
	writeUint64(writer, uint64(uintptr(c.Lower(value))))
}

type FfiDestroyerFeeRate struct{}

func (_ FfiDestroyerFeeRate) Destroy(value *FeeRate) {
	value.Destroy()
}

type FullScanRequestInterface interface {
}
type FullScanRequest struct {
	ffiObject FfiObject
}

func (object *FullScanRequest) Destroy() {
	runtime.SetFinalizer(object, nil)
	object.ffiObject.destroy()
}

type FfiConverterFullScanRequest struct{}

var FfiConverterFullScanRequestINSTANCE = FfiConverterFullScanRequest{}

func (c FfiConverterFullScanRequest) Lift(pointer unsafe.Pointer) *FullScanRequest {
	result := &FullScanRequest{
		newFfiObject(
			pointer,
			func(pointer unsafe.Pointer, status *C.RustCallStatus) unsafe.Pointer {
				return C.uniffi_bdkffi_fn_clone_fullscanrequest(pointer, status)
			},
			func(pointer unsafe.Pointer, status *C.RustCallStatus) {
				C.uniffi_bdkffi_fn_free_fullscanrequest(pointer, status)
			},
		),
	}
	runtime.SetFinalizer(result, (*FullScanRequest).Destroy)
	return result
}

func (c FfiConverterFullScanRequest) Read(reader io.Reader) *FullScanRequest {
	return c.Lift(unsafe.Pointer(uintptr(readUint64(reader))))
}

func (c FfiConverterFullScanRequest) Lower(value *FullScanRequest) unsafe.Pointer {
	// TODO: this is bad - all synchronization from ObjectRuntime.go is discarded here,
	// because the pointer will be decremented immediately after this function returns,
	// and someone will be left holding onto a non-locked pointer.
	pointer := value.ffiObject.incrementPointer("*FullScanRequest")
	defer value.ffiObject.decrementPointer()
	return pointer

}

func (c FfiConverterFullScanRequest) Write(writer io.Writer, value *FullScanRequest) {
	writeUint64(writer, uint64(uintptr(c.Lower(value))))
}

type FfiDestroyerFullScanRequest struct{}

func (_ FfiDestroyerFullScanRequest) Destroy(value *FullScanRequest) {
	value.Destroy()
}

// Builds a [`FullScanRequest`].
type FullScanRequestBuilderInterface interface {
	Build() (*FullScanRequest, error)
	InspectSpksForAllKeychains(inspector FullScanScriptInspector) (*FullScanRequestBuilder, error)
}

// Builds a [`FullScanRequest`].
type FullScanRequestBuilder struct {
	ffiObject FfiObject
}

func (_self *FullScanRequestBuilder) Build() (*FullScanRequest, error) {
	_pointer := _self.ffiObject.incrementPointer("*FullScanRequestBuilder")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[RequestBuilderError](FfiConverterRequestBuilderError{}, func(_uniffiStatus *C.RustCallStatus) unsafe.Pointer {
		return C.uniffi_bdkffi_fn_method_fullscanrequestbuilder_build(
			_pointer, _uniffiStatus)
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue *FullScanRequest
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterFullScanRequestINSTANCE.Lift(_uniffiRV), nil
	}
}

func (_self *FullScanRequestBuilder) InspectSpksForAllKeychains(inspector FullScanScriptInspector) (*FullScanRequestBuilder, error) {
	_pointer := _self.ffiObject.incrementPointer("*FullScanRequestBuilder")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[RequestBuilderError](FfiConverterRequestBuilderError{}, func(_uniffiStatus *C.RustCallStatus) unsafe.Pointer {
		return C.uniffi_bdkffi_fn_method_fullscanrequestbuilder_inspect_spks_for_all_keychains(
			_pointer, FfiConverterFullScanScriptInspectorINSTANCE.Lower(inspector), _uniffiStatus)
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue *FullScanRequestBuilder
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterFullScanRequestBuilderINSTANCE.Lift(_uniffiRV), nil
	}
}
func (object *FullScanRequestBuilder) Destroy() {
	runtime.SetFinalizer(object, nil)
	object.ffiObject.destroy()
}

type FfiConverterFullScanRequestBuilder struct{}

var FfiConverterFullScanRequestBuilderINSTANCE = FfiConverterFullScanRequestBuilder{}

func (c FfiConverterFullScanRequestBuilder) Lift(pointer unsafe.Pointer) *FullScanRequestBuilder {
	result := &FullScanRequestBuilder{
		newFfiObject(
			pointer,
			func(pointer unsafe.Pointer, status *C.RustCallStatus) unsafe.Pointer {
				return C.uniffi_bdkffi_fn_clone_fullscanrequestbuilder(pointer, status)
			},
			func(pointer unsafe.Pointer, status *C.RustCallStatus) {
				C.uniffi_bdkffi_fn_free_fullscanrequestbuilder(pointer, status)
			},
		),
	}
	runtime.SetFinalizer(result, (*FullScanRequestBuilder).Destroy)
	return result
}

func (c FfiConverterFullScanRequestBuilder) Read(reader io.Reader) *FullScanRequestBuilder {
	return c.Lift(unsafe.Pointer(uintptr(readUint64(reader))))
}

func (c FfiConverterFullScanRequestBuilder) Lower(value *FullScanRequestBuilder) unsafe.Pointer {
	// TODO: this is bad - all synchronization from ObjectRuntime.go is discarded here,
	// because the pointer will be decremented immediately after this function returns,
	// and someone will be left holding onto a non-locked pointer.
	pointer := value.ffiObject.incrementPointer("*FullScanRequestBuilder")
	defer value.ffiObject.decrementPointer()
	return pointer

}

func (c FfiConverterFullScanRequestBuilder) Write(writer io.Writer, value *FullScanRequestBuilder) {
	writeUint64(writer, uint64(uintptr(c.Lower(value))))
}

type FfiDestroyerFullScanRequestBuilder struct{}

func (_ FfiDestroyerFullScanRequestBuilder) Destroy(value *FullScanRequestBuilder) {
	value.Destroy()
}

type FullScanScriptInspector interface {
	Inspect(keychain KeychainKind, index uint32, script *Script)
}
type FullScanScriptInspectorImpl struct {
	ffiObject FfiObject
}

func (_self *FullScanScriptInspectorImpl) Inspect(keychain KeychainKind, index uint32, script *Script) {
	_pointer := _self.ffiObject.incrementPointer("FullScanScriptInspector")
	defer _self.ffiObject.decrementPointer()
	rustCall(func(_uniffiStatus *C.RustCallStatus) bool {
		C.uniffi_bdkffi_fn_method_fullscanscriptinspector_inspect(
			_pointer, FfiConverterKeychainKindINSTANCE.Lower(keychain), FfiConverterUint32INSTANCE.Lower(index), FfiConverterScriptINSTANCE.Lower(script), _uniffiStatus)
		return false
	})
}
func (object *FullScanScriptInspectorImpl) Destroy() {
	runtime.SetFinalizer(object, nil)
	object.ffiObject.destroy()
}

type FfiConverterFullScanScriptInspector struct {
	handleMap *concurrentHandleMap[FullScanScriptInspector]
}

var FfiConverterFullScanScriptInspectorINSTANCE = FfiConverterFullScanScriptInspector{
	handleMap: newConcurrentHandleMap[FullScanScriptInspector](),
}

func (c FfiConverterFullScanScriptInspector) Lift(pointer unsafe.Pointer) FullScanScriptInspector {
	result := &FullScanScriptInspectorImpl{
		newFfiObject(
			pointer,
			func(pointer unsafe.Pointer, status *C.RustCallStatus) unsafe.Pointer {
				return C.uniffi_bdkffi_fn_clone_fullscanscriptinspector(pointer, status)
			},
			func(pointer unsafe.Pointer, status *C.RustCallStatus) {
				C.uniffi_bdkffi_fn_free_fullscanscriptinspector(pointer, status)
			},
		),
	}
	runtime.SetFinalizer(result, (*FullScanScriptInspectorImpl).Destroy)
	return result
}

func (c FfiConverterFullScanScriptInspector) Read(reader io.Reader) FullScanScriptInspector {
	return c.Lift(unsafe.Pointer(uintptr(readUint64(reader))))
}

func (c FfiConverterFullScanScriptInspector) Lower(value FullScanScriptInspector) unsafe.Pointer {
	// TODO: this is bad - all synchronization from ObjectRuntime.go is discarded here,
	// because the pointer will be decremented immediately after this function returns,
	// and someone will be left holding onto a non-locked pointer.
	pointer := unsafe.Pointer(uintptr(c.handleMap.insert(value)))
	return pointer

}

func (c FfiConverterFullScanScriptInspector) Write(writer io.Writer, value FullScanScriptInspector) {
	writeUint64(writer, uint64(uintptr(c.Lower(value))))
}

type FfiDestroyerFullScanScriptInspector struct{}

func (_ FfiDestroyerFullScanScriptInspector) Destroy(value FullScanScriptInspector) {
	if val, ok := value.(*FullScanScriptInspectorImpl); ok {
		val.Destroy()
	} else {
		panic("Expected *FullScanScriptInspectorImpl")
	}
}

type uniffiCallbackResult C.int8_t

const (
	uniffiIdxCallbackFree               uniffiCallbackResult = 0
	uniffiCallbackResultSuccess         uniffiCallbackResult = 0
	uniffiCallbackResultError           uniffiCallbackResult = 1
	uniffiCallbackUnexpectedResultError uniffiCallbackResult = 2
	uniffiCallbackCancelled             uniffiCallbackResult = 3
)

type concurrentHandleMap[T any] struct {
	handles       map[uint64]T
	currentHandle uint64
	lock          sync.RWMutex
}

func newConcurrentHandleMap[T any]() *concurrentHandleMap[T] {
	return &concurrentHandleMap[T]{
		handles: map[uint64]T{},
	}
}

func (cm *concurrentHandleMap[T]) insert(obj T) uint64 {
	cm.lock.Lock()
	defer cm.lock.Unlock()

	cm.currentHandle = cm.currentHandle + 1
	cm.handles[cm.currentHandle] = obj
	return cm.currentHandle
}

func (cm *concurrentHandleMap[T]) remove(handle uint64) {
	cm.lock.Lock()
	defer cm.lock.Unlock()

	delete(cm.handles, handle)
}

func (cm *concurrentHandleMap[T]) tryGet(handle uint64) (T, bool) {
	cm.lock.RLock()
	defer cm.lock.RUnlock()

	val, ok := cm.handles[handle]
	return val, ok
}

//export bdkffi_cgo_dispatchCallbackInterfaceFullScanScriptInspectorMethod0
func bdkffi_cgo_dispatchCallbackInterfaceFullScanScriptInspectorMethod0(uniffiHandle C.uint64_t, keychain C.RustBuffer, index C.uint32_t, script unsafe.Pointer, uniffiOutReturn *C.void, callStatus *C.RustCallStatus) {
	handle := uint64(uniffiHandle)
	uniffiObj, ok := FfiConverterFullScanScriptInspectorINSTANCE.handleMap.tryGet(handle)
	if !ok {
		panic(fmt.Errorf("no callback in handle map: %d", handle))
	}

	uniffiObj.Inspect(
		FfiConverterKeychainKindINSTANCE.Lift(GoRustBuffer{
			inner: keychain,
		}),
		FfiConverterUint32INSTANCE.Lift(index),
		FfiConverterScriptINSTANCE.Lift(script),
	)

}

var UniffiVTableCallbackInterfaceFullScanScriptInspectorINSTANCE = C.UniffiVTableCallbackInterfaceFullScanScriptInspector{
	inspect: (C.UniffiCallbackInterfaceFullScanScriptInspectorMethod0)(C.bdkffi_cgo_dispatchCallbackInterfaceFullScanScriptInspectorMethod0),

	uniffiFree: (C.UniffiCallbackInterfaceFree)(C.bdkffi_cgo_dispatchCallbackInterfaceFullScanScriptInspectorFree),
}

//export bdkffi_cgo_dispatchCallbackInterfaceFullScanScriptInspectorFree
func bdkffi_cgo_dispatchCallbackInterfaceFullScanScriptInspectorFree(handle C.uint64_t) {
	FfiConverterFullScanScriptInspectorINSTANCE.handleMap.remove(uint64(handle))
}

func (c FfiConverterFullScanScriptInspector) register() {
	C.uniffi_bdkffi_fn_init_callback_vtable_fullscanscriptinspector(&UniffiVTableCallbackInterfaceFullScanScriptInspectorINSTANCE)
}

type MnemonicInterface interface {
}
type Mnemonic struct {
	ffiObject FfiObject
}

func NewMnemonic(wordCount WordCount) *Mnemonic {
	return FfiConverterMnemonicINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) unsafe.Pointer {
		return C.uniffi_bdkffi_fn_constructor_mnemonic_new(FfiConverterWordCountINSTANCE.Lower(wordCount), _uniffiStatus)
	}))
}

func MnemonicFromEntropy(entropy []uint8) (*Mnemonic, error) {
	_uniffiRV, _uniffiErr := rustCallWithError[Bip39Error](FfiConverterBip39Error{}, func(_uniffiStatus *C.RustCallStatus) unsafe.Pointer {
		return C.uniffi_bdkffi_fn_constructor_mnemonic_from_entropy(FfiConverterSequenceUint8INSTANCE.Lower(entropy), _uniffiStatus)
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue *Mnemonic
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterMnemonicINSTANCE.Lift(_uniffiRV), nil
	}
}

func MnemonicFromString(mnemonic string) (*Mnemonic, error) {
	_uniffiRV, _uniffiErr := rustCallWithError[Bip39Error](FfiConverterBip39Error{}, func(_uniffiStatus *C.RustCallStatus) unsafe.Pointer {
		return C.uniffi_bdkffi_fn_constructor_mnemonic_from_string(FfiConverterStringINSTANCE.Lower(mnemonic), _uniffiStatus)
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue *Mnemonic
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterMnemonicINSTANCE.Lift(_uniffiRV), nil
	}
}

func (_self *Mnemonic) String() string {
	_pointer := _self.ffiObject.incrementPointer("*Mnemonic")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterStringINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_bdkffi_fn_method_mnemonic_uniffi_trait_display(
				_pointer, _uniffiStatus),
		}
	}))
}

func (object *Mnemonic) Destroy() {
	runtime.SetFinalizer(object, nil)
	object.ffiObject.destroy()
}

type FfiConverterMnemonic struct{}

var FfiConverterMnemonicINSTANCE = FfiConverterMnemonic{}

func (c FfiConverterMnemonic) Lift(pointer unsafe.Pointer) *Mnemonic {
	result := &Mnemonic{
		newFfiObject(
			pointer,
			func(pointer unsafe.Pointer, status *C.RustCallStatus) unsafe.Pointer {
				return C.uniffi_bdkffi_fn_clone_mnemonic(pointer, status)
			},
			func(pointer unsafe.Pointer, status *C.RustCallStatus) {
				C.uniffi_bdkffi_fn_free_mnemonic(pointer, status)
			},
		),
	}
	runtime.SetFinalizer(result, (*Mnemonic).Destroy)
	return result
}

func (c FfiConverterMnemonic) Read(reader io.Reader) *Mnemonic {
	return c.Lift(unsafe.Pointer(uintptr(readUint64(reader))))
}

func (c FfiConverterMnemonic) Lower(value *Mnemonic) unsafe.Pointer {
	// TODO: this is bad - all synchronization from ObjectRuntime.go is discarded here,
	// because the pointer will be decremented immediately after this function returns,
	// and someone will be left holding onto a non-locked pointer.
	pointer := value.ffiObject.incrementPointer("*Mnemonic")
	defer value.ffiObject.decrementPointer()
	return pointer

}

func (c FfiConverterMnemonic) Write(writer io.Writer, value *Mnemonic) {
	writeUint64(writer, uint64(uintptr(c.Lower(value))))
}

type FfiDestroyerMnemonic struct{}

func (_ FfiDestroyerMnemonic) Destroy(value *Mnemonic) {
	value.Destroy()
}

type PolicyInterface interface {
	AsString() string
	Contribution() Satisfaction
	Id() string
	Item() SatisfiableItem
	RequiresPath() bool
	Satisfaction() Satisfaction
}
type Policy struct {
	ffiObject FfiObject
}

func (_self *Policy) AsString() string {
	_pointer := _self.ffiObject.incrementPointer("*Policy")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterStringINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_bdkffi_fn_method_policy_as_string(
				_pointer, _uniffiStatus),
		}
	}))
}

func (_self *Policy) Contribution() Satisfaction {
	_pointer := _self.ffiObject.incrementPointer("*Policy")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterSatisfactionINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_bdkffi_fn_method_policy_contribution(
				_pointer, _uniffiStatus),
		}
	}))
}

func (_self *Policy) Id() string {
	_pointer := _self.ffiObject.incrementPointer("*Policy")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterStringINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_bdkffi_fn_method_policy_id(
				_pointer, _uniffiStatus),
		}
	}))
}

func (_self *Policy) Item() SatisfiableItem {
	_pointer := _self.ffiObject.incrementPointer("*Policy")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterSatisfiableItemINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_bdkffi_fn_method_policy_item(
				_pointer, _uniffiStatus),
		}
	}))
}

func (_self *Policy) RequiresPath() bool {
	_pointer := _self.ffiObject.incrementPointer("*Policy")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterBoolINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) C.int8_t {
		return C.uniffi_bdkffi_fn_method_policy_requires_path(
			_pointer, _uniffiStatus)
	}))
}

func (_self *Policy) Satisfaction() Satisfaction {
	_pointer := _self.ffiObject.incrementPointer("*Policy")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterSatisfactionINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_bdkffi_fn_method_policy_satisfaction(
				_pointer, _uniffiStatus),
		}
	}))
}
func (object *Policy) Destroy() {
	runtime.SetFinalizer(object, nil)
	object.ffiObject.destroy()
}

type FfiConverterPolicy struct{}

var FfiConverterPolicyINSTANCE = FfiConverterPolicy{}

func (c FfiConverterPolicy) Lift(pointer unsafe.Pointer) *Policy {
	result := &Policy{
		newFfiObject(
			pointer,
			func(pointer unsafe.Pointer, status *C.RustCallStatus) unsafe.Pointer {
				return C.uniffi_bdkffi_fn_clone_policy(pointer, status)
			},
			func(pointer unsafe.Pointer, status *C.RustCallStatus) {
				C.uniffi_bdkffi_fn_free_policy(pointer, status)
			},
		),
	}
	runtime.SetFinalizer(result, (*Policy).Destroy)
	return result
}

func (c FfiConverterPolicy) Read(reader io.Reader) *Policy {
	return c.Lift(unsafe.Pointer(uintptr(readUint64(reader))))
}

func (c FfiConverterPolicy) Lower(value *Policy) unsafe.Pointer {
	// TODO: this is bad - all synchronization from ObjectRuntime.go is discarded here,
	// because the pointer will be decremented immediately after this function returns,
	// and someone will be left holding onto a non-locked pointer.
	pointer := value.ffiObject.incrementPointer("*Policy")
	defer value.ffiObject.decrementPointer()
	return pointer

}

func (c FfiConverterPolicy) Write(writer io.Writer, value *Policy) {
	writeUint64(writer, uint64(uintptr(c.Lower(value))))
}

type FfiDestroyerPolicy struct{}

func (_ FfiDestroyerPolicy) Destroy(value *Policy) {
	value.Destroy()
}

type PsbtInterface interface {
	Combine(other *Psbt) (*Psbt, error)
	ExtractTx() (*Transaction, error)
	Fee() (uint64, error)
	Finalize() FinalizedPsbtResult
	JsonSerialize() string
	Serialize() string
}
type Psbt struct {
	ffiObject FfiObject
}

func NewPsbt(psbtBase64 string) (*Psbt, error) {
	_uniffiRV, _uniffiErr := rustCallWithError[PsbtParseError](FfiConverterPsbtParseError{}, func(_uniffiStatus *C.RustCallStatus) unsafe.Pointer {
		return C.uniffi_bdkffi_fn_constructor_psbt_new(FfiConverterStringINSTANCE.Lower(psbtBase64), _uniffiStatus)
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue *Psbt
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterPsbtINSTANCE.Lift(_uniffiRV), nil
	}
}

func (_self *Psbt) Combine(other *Psbt) (*Psbt, error) {
	_pointer := _self.ffiObject.incrementPointer("*Psbt")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[PsbtError](FfiConverterPsbtError{}, func(_uniffiStatus *C.RustCallStatus) unsafe.Pointer {
		return C.uniffi_bdkffi_fn_method_psbt_combine(
			_pointer, FfiConverterPsbtINSTANCE.Lower(other), _uniffiStatus)
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue *Psbt
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterPsbtINSTANCE.Lift(_uniffiRV), nil
	}
}

func (_self *Psbt) ExtractTx() (*Transaction, error) {
	_pointer := _self.ffiObject.incrementPointer("*Psbt")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[ExtractTxError](FfiConverterExtractTxError{}, func(_uniffiStatus *C.RustCallStatus) unsafe.Pointer {
		return C.uniffi_bdkffi_fn_method_psbt_extract_tx(
			_pointer, _uniffiStatus)
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue *Transaction
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterTransactionINSTANCE.Lift(_uniffiRV), nil
	}
}

func (_self *Psbt) Fee() (uint64, error) {
	_pointer := _self.ffiObject.incrementPointer("*Psbt")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[PsbtError](FfiConverterPsbtError{}, func(_uniffiStatus *C.RustCallStatus) C.uint64_t {
		return C.uniffi_bdkffi_fn_method_psbt_fee(
			_pointer, _uniffiStatus)
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue uint64
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterUint64INSTANCE.Lift(_uniffiRV), nil
	}
}

func (_self *Psbt) Finalize() FinalizedPsbtResult {
	_pointer := _self.ffiObject.incrementPointer("*Psbt")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterFinalizedPsbtResultINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_bdkffi_fn_method_psbt_finalize(
				_pointer, _uniffiStatus),
		}
	}))
}

func (_self *Psbt) JsonSerialize() string {
	_pointer := _self.ffiObject.incrementPointer("*Psbt")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterStringINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_bdkffi_fn_method_psbt_json_serialize(
				_pointer, _uniffiStatus),
		}
	}))
}

func (_self *Psbt) Serialize() string {
	_pointer := _self.ffiObject.incrementPointer("*Psbt")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterStringINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_bdkffi_fn_method_psbt_serialize(
				_pointer, _uniffiStatus),
		}
	}))
}
func (object *Psbt) Destroy() {
	runtime.SetFinalizer(object, nil)
	object.ffiObject.destroy()
}

type FfiConverterPsbt struct{}

var FfiConverterPsbtINSTANCE = FfiConverterPsbt{}

func (c FfiConverterPsbt) Lift(pointer unsafe.Pointer) *Psbt {
	result := &Psbt{
		newFfiObject(
			pointer,
			func(pointer unsafe.Pointer, status *C.RustCallStatus) unsafe.Pointer {
				return C.uniffi_bdkffi_fn_clone_psbt(pointer, status)
			},
			func(pointer unsafe.Pointer, status *C.RustCallStatus) {
				C.uniffi_bdkffi_fn_free_psbt(pointer, status)
			},
		),
	}
	runtime.SetFinalizer(result, (*Psbt).Destroy)
	return result
}

func (c FfiConverterPsbt) Read(reader io.Reader) *Psbt {
	return c.Lift(unsafe.Pointer(uintptr(readUint64(reader))))
}

func (c FfiConverterPsbt) Lower(value *Psbt) unsafe.Pointer {
	// TODO: this is bad - all synchronization from ObjectRuntime.go is discarded here,
	// because the pointer will be decremented immediately after this function returns,
	// and someone will be left holding onto a non-locked pointer.
	pointer := value.ffiObject.incrementPointer("*Psbt")
	defer value.ffiObject.decrementPointer()
	return pointer

}

func (c FfiConverterPsbt) Write(writer io.Writer, value *Psbt) {
	writeUint64(writer, uint64(uintptr(c.Lower(value))))
}

type FfiDestroyerPsbt struct{}

func (_ FfiDestroyerPsbt) Destroy(value *Psbt) {
	value.Destroy()
}

type ScriptInterface interface {
	ToBytes() []uint8
}
type Script struct {
	ffiObject FfiObject
}

func NewScript(rawOutputScript []uint8) *Script {
	return FfiConverterScriptINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) unsafe.Pointer {
		return C.uniffi_bdkffi_fn_constructor_script_new(FfiConverterSequenceUint8INSTANCE.Lower(rawOutputScript), _uniffiStatus)
	}))
}

func (_self *Script) ToBytes() []uint8 {
	_pointer := _self.ffiObject.incrementPointer("*Script")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterSequenceUint8INSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_bdkffi_fn_method_script_to_bytes(
				_pointer, _uniffiStatus),
		}
	}))
}
func (object *Script) Destroy() {
	runtime.SetFinalizer(object, nil)
	object.ffiObject.destroy()
}

type FfiConverterScript struct{}

var FfiConverterScriptINSTANCE = FfiConverterScript{}

func (c FfiConverterScript) Lift(pointer unsafe.Pointer) *Script {
	result := &Script{
		newFfiObject(
			pointer,
			func(pointer unsafe.Pointer, status *C.RustCallStatus) unsafe.Pointer {
				return C.uniffi_bdkffi_fn_clone_script(pointer, status)
			},
			func(pointer unsafe.Pointer, status *C.RustCallStatus) {
				C.uniffi_bdkffi_fn_free_script(pointer, status)
			},
		),
	}
	runtime.SetFinalizer(result, (*Script).Destroy)
	return result
}

func (c FfiConverterScript) Read(reader io.Reader) *Script {
	return c.Lift(unsafe.Pointer(uintptr(readUint64(reader))))
}

func (c FfiConverterScript) Lower(value *Script) unsafe.Pointer {
	// TODO: this is bad - all synchronization from ObjectRuntime.go is discarded here,
	// because the pointer will be decremented immediately after this function returns,
	// and someone will be left holding onto a non-locked pointer.
	pointer := value.ffiObject.incrementPointer("*Script")
	defer value.ffiObject.decrementPointer()
	return pointer

}

func (c FfiConverterScript) Write(writer io.Writer, value *Script) {
	writeUint64(writer, uint64(uintptr(c.Lower(value))))
}

type FfiDestroyerScript struct{}

func (_ FfiDestroyerScript) Destroy(value *Script) {
	value.Destroy()
}

type SyncRequestInterface interface {
}
type SyncRequest struct {
	ffiObject FfiObject
}

func (object *SyncRequest) Destroy() {
	runtime.SetFinalizer(object, nil)
	object.ffiObject.destroy()
}

type FfiConverterSyncRequest struct{}

var FfiConverterSyncRequestINSTANCE = FfiConverterSyncRequest{}

func (c FfiConverterSyncRequest) Lift(pointer unsafe.Pointer) *SyncRequest {
	result := &SyncRequest{
		newFfiObject(
			pointer,
			func(pointer unsafe.Pointer, status *C.RustCallStatus) unsafe.Pointer {
				return C.uniffi_bdkffi_fn_clone_syncrequest(pointer, status)
			},
			func(pointer unsafe.Pointer, status *C.RustCallStatus) {
				C.uniffi_bdkffi_fn_free_syncrequest(pointer, status)
			},
		),
	}
	runtime.SetFinalizer(result, (*SyncRequest).Destroy)
	return result
}

func (c FfiConverterSyncRequest) Read(reader io.Reader) *SyncRequest {
	return c.Lift(unsafe.Pointer(uintptr(readUint64(reader))))
}

func (c FfiConverterSyncRequest) Lower(value *SyncRequest) unsafe.Pointer {
	// TODO: this is bad - all synchronization from ObjectRuntime.go is discarded here,
	// because the pointer will be decremented immediately after this function returns,
	// and someone will be left holding onto a non-locked pointer.
	pointer := value.ffiObject.incrementPointer("*SyncRequest")
	defer value.ffiObject.decrementPointer()
	return pointer

}

func (c FfiConverterSyncRequest) Write(writer io.Writer, value *SyncRequest) {
	writeUint64(writer, uint64(uintptr(c.Lower(value))))
}

type FfiDestroyerSyncRequest struct{}

func (_ FfiDestroyerSyncRequest) Destroy(value *SyncRequest) {
	value.Destroy()
}

// Builds a [`SyncRequest`].
type SyncRequestBuilderInterface interface {
	Build() (*SyncRequest, error)
	InspectSpks(inspector SyncScriptInspector) (*SyncRequestBuilder, error)
}

// Builds a [`SyncRequest`].
type SyncRequestBuilder struct {
	ffiObject FfiObject
}

func (_self *SyncRequestBuilder) Build() (*SyncRequest, error) {
	_pointer := _self.ffiObject.incrementPointer("*SyncRequestBuilder")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[RequestBuilderError](FfiConverterRequestBuilderError{}, func(_uniffiStatus *C.RustCallStatus) unsafe.Pointer {
		return C.uniffi_bdkffi_fn_method_syncrequestbuilder_build(
			_pointer, _uniffiStatus)
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue *SyncRequest
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterSyncRequestINSTANCE.Lift(_uniffiRV), nil
	}
}

func (_self *SyncRequestBuilder) InspectSpks(inspector SyncScriptInspector) (*SyncRequestBuilder, error) {
	_pointer := _self.ffiObject.incrementPointer("*SyncRequestBuilder")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[RequestBuilderError](FfiConverterRequestBuilderError{}, func(_uniffiStatus *C.RustCallStatus) unsafe.Pointer {
		return C.uniffi_bdkffi_fn_method_syncrequestbuilder_inspect_spks(
			_pointer, FfiConverterSyncScriptInspectorINSTANCE.Lower(inspector), _uniffiStatus)
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue *SyncRequestBuilder
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterSyncRequestBuilderINSTANCE.Lift(_uniffiRV), nil
	}
}
func (object *SyncRequestBuilder) Destroy() {
	runtime.SetFinalizer(object, nil)
	object.ffiObject.destroy()
}

type FfiConverterSyncRequestBuilder struct{}

var FfiConverterSyncRequestBuilderINSTANCE = FfiConverterSyncRequestBuilder{}

func (c FfiConverterSyncRequestBuilder) Lift(pointer unsafe.Pointer) *SyncRequestBuilder {
	result := &SyncRequestBuilder{
		newFfiObject(
			pointer,
			func(pointer unsafe.Pointer, status *C.RustCallStatus) unsafe.Pointer {
				return C.uniffi_bdkffi_fn_clone_syncrequestbuilder(pointer, status)
			},
			func(pointer unsafe.Pointer, status *C.RustCallStatus) {
				C.uniffi_bdkffi_fn_free_syncrequestbuilder(pointer, status)
			},
		),
	}
	runtime.SetFinalizer(result, (*SyncRequestBuilder).Destroy)
	return result
}

func (c FfiConverterSyncRequestBuilder) Read(reader io.Reader) *SyncRequestBuilder {
	return c.Lift(unsafe.Pointer(uintptr(readUint64(reader))))
}

func (c FfiConverterSyncRequestBuilder) Lower(value *SyncRequestBuilder) unsafe.Pointer {
	// TODO: this is bad - all synchronization from ObjectRuntime.go is discarded here,
	// because the pointer will be decremented immediately after this function returns,
	// and someone will be left holding onto a non-locked pointer.
	pointer := value.ffiObject.incrementPointer("*SyncRequestBuilder")
	defer value.ffiObject.decrementPointer()
	return pointer

}

func (c FfiConverterSyncRequestBuilder) Write(writer io.Writer, value *SyncRequestBuilder) {
	writeUint64(writer, uint64(uintptr(c.Lower(value))))
}

type FfiDestroyerSyncRequestBuilder struct{}

func (_ FfiDestroyerSyncRequestBuilder) Destroy(value *SyncRequestBuilder) {
	value.Destroy()
}

type SyncScriptInspector interface {
	Inspect(script *Script, total uint64)
}
type SyncScriptInspectorImpl struct {
	ffiObject FfiObject
}

func (_self *SyncScriptInspectorImpl) Inspect(script *Script, total uint64) {
	_pointer := _self.ffiObject.incrementPointer("SyncScriptInspector")
	defer _self.ffiObject.decrementPointer()
	rustCall(func(_uniffiStatus *C.RustCallStatus) bool {
		C.uniffi_bdkffi_fn_method_syncscriptinspector_inspect(
			_pointer, FfiConverterScriptINSTANCE.Lower(script), FfiConverterUint64INSTANCE.Lower(total), _uniffiStatus)
		return false
	})
}
func (object *SyncScriptInspectorImpl) Destroy() {
	runtime.SetFinalizer(object, nil)
	object.ffiObject.destroy()
}

type FfiConverterSyncScriptInspector struct {
	handleMap *concurrentHandleMap[SyncScriptInspector]
}

var FfiConverterSyncScriptInspectorINSTANCE = FfiConverterSyncScriptInspector{
	handleMap: newConcurrentHandleMap[SyncScriptInspector](),
}

func (c FfiConverterSyncScriptInspector) Lift(pointer unsafe.Pointer) SyncScriptInspector {
	result := &SyncScriptInspectorImpl{
		newFfiObject(
			pointer,
			func(pointer unsafe.Pointer, status *C.RustCallStatus) unsafe.Pointer {
				return C.uniffi_bdkffi_fn_clone_syncscriptinspector(pointer, status)
			},
			func(pointer unsafe.Pointer, status *C.RustCallStatus) {
				C.uniffi_bdkffi_fn_free_syncscriptinspector(pointer, status)
			},
		),
	}
	runtime.SetFinalizer(result, (*SyncScriptInspectorImpl).Destroy)
	return result
}

func (c FfiConverterSyncScriptInspector) Read(reader io.Reader) SyncScriptInspector {
	return c.Lift(unsafe.Pointer(uintptr(readUint64(reader))))
}

func (c FfiConverterSyncScriptInspector) Lower(value SyncScriptInspector) unsafe.Pointer {
	// TODO: this is bad - all synchronization from ObjectRuntime.go is discarded here,
	// because the pointer will be decremented immediately after this function returns,
	// and someone will be left holding onto a non-locked pointer.
	pointer := unsafe.Pointer(uintptr(c.handleMap.insert(value)))
	return pointer

}

func (c FfiConverterSyncScriptInspector) Write(writer io.Writer, value SyncScriptInspector) {
	writeUint64(writer, uint64(uintptr(c.Lower(value))))
}

type FfiDestroyerSyncScriptInspector struct{}

func (_ FfiDestroyerSyncScriptInspector) Destroy(value SyncScriptInspector) {
	if val, ok := value.(*SyncScriptInspectorImpl); ok {
		val.Destroy()
	} else {
		panic("Expected *SyncScriptInspectorImpl")
	}
}

//export bdkffi_cgo_dispatchCallbackInterfaceSyncScriptInspectorMethod0
func bdkffi_cgo_dispatchCallbackInterfaceSyncScriptInspectorMethod0(uniffiHandle C.uint64_t, script unsafe.Pointer, total C.uint64_t, uniffiOutReturn *C.void, callStatus *C.RustCallStatus) {
	handle := uint64(uniffiHandle)
	uniffiObj, ok := FfiConverterSyncScriptInspectorINSTANCE.handleMap.tryGet(handle)
	if !ok {
		panic(fmt.Errorf("no callback in handle map: %d", handle))
	}

	uniffiObj.Inspect(
		FfiConverterScriptINSTANCE.Lift(script),
		FfiConverterUint64INSTANCE.Lift(total),
	)

}

var UniffiVTableCallbackInterfaceSyncScriptInspectorINSTANCE = C.UniffiVTableCallbackInterfaceSyncScriptInspector{
	inspect: (C.UniffiCallbackInterfaceSyncScriptInspectorMethod0)(C.bdkffi_cgo_dispatchCallbackInterfaceSyncScriptInspectorMethod0),

	uniffiFree: (C.UniffiCallbackInterfaceFree)(C.bdkffi_cgo_dispatchCallbackInterfaceSyncScriptInspectorFree),
}

//export bdkffi_cgo_dispatchCallbackInterfaceSyncScriptInspectorFree
func bdkffi_cgo_dispatchCallbackInterfaceSyncScriptInspectorFree(handle C.uint64_t) {
	FfiConverterSyncScriptInspectorINSTANCE.handleMap.remove(uint64(handle))
}

func (c FfiConverterSyncScriptInspector) register() {
	C.uniffi_bdkffi_fn_init_callback_vtable_syncscriptinspector(&UniffiVTableCallbackInterfaceSyncScriptInspectorINSTANCE)
}

type TransactionInterface interface {
	ComputeTxid() string
	Input() []TxIn
	IsCoinbase() bool
	IsExplicitlyRbf() bool
	IsLockTimeEnabled() bool
	LockTime() uint32
	Output() []TxOut
	Serialize() []uint8
	TotalSize() uint64
	Version() int32
	Vsize() uint64
	Weight() uint64
}
type Transaction struct {
	ffiObject FfiObject
}

func NewTransaction(transactionBytes []uint8) (*Transaction, error) {
	_uniffiRV, _uniffiErr := rustCallWithError[TransactionError](FfiConverterTransactionError{}, func(_uniffiStatus *C.RustCallStatus) unsafe.Pointer {
		return C.uniffi_bdkffi_fn_constructor_transaction_new(FfiConverterSequenceUint8INSTANCE.Lower(transactionBytes), _uniffiStatus)
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue *Transaction
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterTransactionINSTANCE.Lift(_uniffiRV), nil
	}
}

func (_self *Transaction) ComputeTxid() string {
	_pointer := _self.ffiObject.incrementPointer("*Transaction")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterStringINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_bdkffi_fn_method_transaction_compute_txid(
				_pointer, _uniffiStatus),
		}
	}))
}

func (_self *Transaction) Input() []TxIn {
	_pointer := _self.ffiObject.incrementPointer("*Transaction")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterSequenceTxInINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_bdkffi_fn_method_transaction_input(
				_pointer, _uniffiStatus),
		}
	}))
}

func (_self *Transaction) IsCoinbase() bool {
	_pointer := _self.ffiObject.incrementPointer("*Transaction")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterBoolINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) C.int8_t {
		return C.uniffi_bdkffi_fn_method_transaction_is_coinbase(
			_pointer, _uniffiStatus)
	}))
}

func (_self *Transaction) IsExplicitlyRbf() bool {
	_pointer := _self.ffiObject.incrementPointer("*Transaction")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterBoolINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) C.int8_t {
		return C.uniffi_bdkffi_fn_method_transaction_is_explicitly_rbf(
			_pointer, _uniffiStatus)
	}))
}

func (_self *Transaction) IsLockTimeEnabled() bool {
	_pointer := _self.ffiObject.incrementPointer("*Transaction")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterBoolINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) C.int8_t {
		return C.uniffi_bdkffi_fn_method_transaction_is_lock_time_enabled(
			_pointer, _uniffiStatus)
	}))
}

func (_self *Transaction) LockTime() uint32 {
	_pointer := _self.ffiObject.incrementPointer("*Transaction")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterUint32INSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint32_t {
		return C.uniffi_bdkffi_fn_method_transaction_lock_time(
			_pointer, _uniffiStatus)
	}))
}

func (_self *Transaction) Output() []TxOut {
	_pointer := _self.ffiObject.incrementPointer("*Transaction")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterSequenceTxOutINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_bdkffi_fn_method_transaction_output(
				_pointer, _uniffiStatus),
		}
	}))
}

func (_self *Transaction) Serialize() []uint8 {
	_pointer := _self.ffiObject.incrementPointer("*Transaction")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterSequenceUint8INSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_bdkffi_fn_method_transaction_serialize(
				_pointer, _uniffiStatus),
		}
	}))
}

func (_self *Transaction) TotalSize() uint64 {
	_pointer := _self.ffiObject.incrementPointer("*Transaction")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterUint64INSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint64_t {
		return C.uniffi_bdkffi_fn_method_transaction_total_size(
			_pointer, _uniffiStatus)
	}))
}

func (_self *Transaction) Version() int32 {
	_pointer := _self.ffiObject.incrementPointer("*Transaction")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterInt32INSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) C.int32_t {
		return C.uniffi_bdkffi_fn_method_transaction_version(
			_pointer, _uniffiStatus)
	}))
}

func (_self *Transaction) Vsize() uint64 {
	_pointer := _self.ffiObject.incrementPointer("*Transaction")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterUint64INSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint64_t {
		return C.uniffi_bdkffi_fn_method_transaction_vsize(
			_pointer, _uniffiStatus)
	}))
}

func (_self *Transaction) Weight() uint64 {
	_pointer := _self.ffiObject.incrementPointer("*Transaction")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterUint64INSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint64_t {
		return C.uniffi_bdkffi_fn_method_transaction_weight(
			_pointer, _uniffiStatus)
	}))
}
func (object *Transaction) Destroy() {
	runtime.SetFinalizer(object, nil)
	object.ffiObject.destroy()
}

type FfiConverterTransaction struct{}

var FfiConverterTransactionINSTANCE = FfiConverterTransaction{}

func (c FfiConverterTransaction) Lift(pointer unsafe.Pointer) *Transaction {
	result := &Transaction{
		newFfiObject(
			pointer,
			func(pointer unsafe.Pointer, status *C.RustCallStatus) unsafe.Pointer {
				return C.uniffi_bdkffi_fn_clone_transaction(pointer, status)
			},
			func(pointer unsafe.Pointer, status *C.RustCallStatus) {
				C.uniffi_bdkffi_fn_free_transaction(pointer, status)
			},
		),
	}
	runtime.SetFinalizer(result, (*Transaction).Destroy)
	return result
}

func (c FfiConverterTransaction) Read(reader io.Reader) *Transaction {
	return c.Lift(unsafe.Pointer(uintptr(readUint64(reader))))
}

func (c FfiConverterTransaction) Lower(value *Transaction) unsafe.Pointer {
	// TODO: this is bad - all synchronization from ObjectRuntime.go is discarded here,
	// because the pointer will be decremented immediately after this function returns,
	// and someone will be left holding onto a non-locked pointer.
	pointer := value.ffiObject.incrementPointer("*Transaction")
	defer value.ffiObject.decrementPointer()
	return pointer

}

func (c FfiConverterTransaction) Write(writer io.Writer, value *Transaction) {
	writeUint64(writer, uint64(uintptr(c.Lower(value))))
}

type FfiDestroyerTransaction struct{}

func (_ FfiDestroyerTransaction) Destroy(value *Transaction) {
	value.Destroy()
}

// A `TxBuilder` is created by calling `build_tx` on a wallet. After assigning it, you set options on it until finally
// calling `finish` to consume the builder and generate the transaction.
type TxBuilderInterface interface {
	// Add data as an output using `OP_RETURN`.
	AddData(data []uint8) *TxBuilder
	// Fill-in the `PSBT_GLOBAL_XPUB` field with the extended keys contained in both the external and internal
	// descriptors.
	//
	// This is useful for offline signers that take part to a multisig. Some hardware wallets like BitBox and ColdCard
	// are known to require this.
	AddGlobalXpubs() *TxBuilder
	// Add a recipient to the internal list of recipients.
	AddRecipient(script *Script, amount *Amount) *TxBuilder
	// Add a utxo to the internal list of unspendable utxos.
	//
	// It’s important to note that the "must-be-spent" utxos added with `TxBuilder::add_utxo` have priority over this.
	AddUnspendable(unspendable OutPoint) *TxBuilder
	// Add a utxo to the internal list of utxos that must be spent.
	//
	// These have priority over the "unspendable" utxos, meaning that if a utxo is present both in the "utxos" and the
	// "unspendable" list, it will be spent.
	AddUtxo(outpoint OutPoint) *TxBuilder
	// Set whether or not the dust limit is checked.
	//
	// Note: by avoiding a dust limit check you may end up with a transaction that is non-standard.
	AllowDust(allowDust bool) *TxBuilder
	// Set a specific `ChangeSpendPolicy`. See `TxBuilder::do_not_spend_change` and `TxBuilder::only_spend_change` for
	// some shortcuts. This method assumes the presence of an internal keychain, otherwise it has no effect.
	ChangePolicy(changePolicy ChangeSpendPolicy) *TxBuilder
	// Set the current blockchain height.
	//
	// This will be used to:
	//
	// 1. Set the `nLockTime` for preventing fee sniping. Note: This will be ignored if you manually specify a
	// `nlocktime` using `TxBuilder::nlocktime`.
	//
	// 2. Decide whether coinbase outputs are mature or not. If the coinbase outputs are not mature at `current_height`,
	// we ignore them in the coin selection. If you want to create a transaction that spends immature coinbase inputs,
	// manually add them using `TxBuilder::add_utxos`.
	// In both cases, if you don’t provide a current height, we use the last sync height.
	CurrentHeight(height uint32) *TxBuilder
	// Do not spend change outputs.
	//
	// This effectively adds all the change outputs to the "unspendable" list. See `TxBuilder::unspendable`. This method
	// assumes the presence of an internal keychain, otherwise it has no effect.
	DoNotSpendChange() *TxBuilder
	// Sets the address to drain excess coins to.
	//
	// Usually, when there are excess coins they are sent to a change address generated by the wallet. This option
	// replaces the usual change address with an arbitrary script_pubkey of your choosing. Just as with a change output,
	// if the drain output is not needed (the excess coins are too small) it will not be included in the resulting
	// transaction. The only difference is that it is valid to use `drain_to` without setting any ordinary recipients
	// with `add_recipient` (but it is perfectly fine to add recipients as well).
	//
	// If you choose not to set any recipients, you should provide the utxos that the transaction should spend via
	// `add_utxos`. `drain_to` is very useful for draining all the coins in a wallet with `drain_wallet` to a single
	// address.
	DrainTo(script *Script) *TxBuilder
	// Spend all the available inputs. This respects filters like `TxBuilder::unspendable` and the change policy.
	DrainWallet() *TxBuilder
	// Set an absolute fee The `fee_absolute` method refers to the absolute transaction fee in `Amount`. If anyone sets
	// both the `fee_absolute` method and the `fee_rate` method, the `FeePolicy` enum will be set by whichever method was
	// called last, as the `FeeRate` and `FeeAmount` are mutually exclusive.
	//
	// Note that this is really a minimum absolute fee – it’s possible to overshoot it slightly since adding a change output to drain the remaining excess might not be viable.
	FeeAbsolute(fee *Amount) *TxBuilder
	// Set a custom fee rate.
	//
	// This method sets the mining fee paid by the transaction as a rate on its size. This means that the total fee paid
	// is equal to fee_rate times the size of the transaction. Default is 1 sat/vB in accordance with Bitcoin Core’s
	// default relay policy.
	//
	// Note that this is really a minimum feerate – it’s possible to overshoot it slightly since adding a change output
	// to drain the remaining excess might not be viable.
	FeeRate(feeRate *FeeRate) *TxBuilder
	// Finish building the transaction.
	//
	// Uses the thread-local random number generator (rng).
	//
	// Returns a new `Psbt` per BIP174.
	//
	// WARNING: To avoid change address reuse you must persist the changes resulting from one or more calls to this
	// method before closing the wallet. See `Wallet::reveal_next_address`.
	Finish(wallet *Wallet) (*Psbt, error)
	// Only spend utxos added by `TxBuilder::add_utxo`.
	//
	// The wallet will not add additional utxos to the transaction even if they are needed to make the transaction valid.
	ManuallySelectedOnly() *TxBuilder
	// Use a specific nLockTime while creating the transaction.
	//
	// This can cause conflicts if the wallet’s descriptors contain an "after" (`OP_CLTV`) operator.
	Nlocktime(locktime LockTime) *TxBuilder
	// Only spend change outputs.
	//
	// This effectively adds all the non-change outputs to the "unspendable" list. See `TxBuilder::unspendable`. This
	// method assumes the presence of an internal keychain, otherwise it has no effect.
	OnlySpendChange() *TxBuilder
	// The TxBuilder::policy_path is a complex API. See the Rust docs for complete information: https://docs.rs/bdk_wallet/latest/bdk_wallet/struct.TxBuilder.html#method.policy_path
	PolicyPath(policyPath map[string][]uint64, keychain KeychainKind) *TxBuilder
	// Set an exact `nSequence` value.
	//
	// This can cause conflicts if the wallet’s descriptors contain an "older" (`OP_CSV`) operator and the given
	// `nsequence` is lower than the CSV value.
	SetExactSequence(nsequence uint32) *TxBuilder
	// Replace the recipients already added with a new list of recipients.
	SetRecipients(recipients []ScriptAmount) *TxBuilder
	// Replace the internal list of unspendable utxos with a new list.
	//
	// It’s important to note that the "must-be-spent" utxos added with `TxBuilder::add_utxo` have priority over these.
	Unspendable(unspendable []OutPoint) *TxBuilder
	// Build a transaction with a specific version.
	//
	// The version should always be greater than 0 and greater than 1 if the wallet’s descriptors contain an "older"
	// (`OP_CSV`) operator.
	Version(version int32) *TxBuilder
}

// A `TxBuilder` is created by calling `build_tx` on a wallet. After assigning it, you set options on it until finally
// calling `finish` to consume the builder and generate the transaction.
type TxBuilder struct {
	ffiObject FfiObject
}

func NewTxBuilder() *TxBuilder {
	return FfiConverterTxBuilderINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) unsafe.Pointer {
		return C.uniffi_bdkffi_fn_constructor_txbuilder_new(_uniffiStatus)
	}))
}

// Add data as an output using `OP_RETURN`.
func (_self *TxBuilder) AddData(data []uint8) *TxBuilder {
	_pointer := _self.ffiObject.incrementPointer("*TxBuilder")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterTxBuilderINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) unsafe.Pointer {
		return C.uniffi_bdkffi_fn_method_txbuilder_add_data(
			_pointer, FfiConverterSequenceUint8INSTANCE.Lower(data), _uniffiStatus)
	}))
}

// Fill-in the `PSBT_GLOBAL_XPUB` field with the extended keys contained in both the external and internal
// descriptors.
//
// This is useful for offline signers that take part to a multisig. Some hardware wallets like BitBox and ColdCard
// are known to require this.
func (_self *TxBuilder) AddGlobalXpubs() *TxBuilder {
	_pointer := _self.ffiObject.incrementPointer("*TxBuilder")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterTxBuilderINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) unsafe.Pointer {
		return C.uniffi_bdkffi_fn_method_txbuilder_add_global_xpubs(
			_pointer, _uniffiStatus)
	}))
}

// Add a recipient to the internal list of recipients.
func (_self *TxBuilder) AddRecipient(script *Script, amount *Amount) *TxBuilder {
	_pointer := _self.ffiObject.incrementPointer("*TxBuilder")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterTxBuilderINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) unsafe.Pointer {
		return C.uniffi_bdkffi_fn_method_txbuilder_add_recipient(
			_pointer, FfiConverterScriptINSTANCE.Lower(script), FfiConverterAmountINSTANCE.Lower(amount), _uniffiStatus)
	}))
}

// Add a utxo to the internal list of unspendable utxos.
//
// It’s important to note that the "must-be-spent" utxos added with `TxBuilder::add_utxo` have priority over this.
func (_self *TxBuilder) AddUnspendable(unspendable OutPoint) *TxBuilder {
	_pointer := _self.ffiObject.incrementPointer("*TxBuilder")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterTxBuilderINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) unsafe.Pointer {
		return C.uniffi_bdkffi_fn_method_txbuilder_add_unspendable(
			_pointer, FfiConverterOutPointINSTANCE.Lower(unspendable), _uniffiStatus)
	}))
}

// Add a utxo to the internal list of utxos that must be spent.
//
// These have priority over the "unspendable" utxos, meaning that if a utxo is present both in the "utxos" and the
// "unspendable" list, it will be spent.
func (_self *TxBuilder) AddUtxo(outpoint OutPoint) *TxBuilder {
	_pointer := _self.ffiObject.incrementPointer("*TxBuilder")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterTxBuilderINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) unsafe.Pointer {
		return C.uniffi_bdkffi_fn_method_txbuilder_add_utxo(
			_pointer, FfiConverterOutPointINSTANCE.Lower(outpoint), _uniffiStatus)
	}))
}

// Set whether or not the dust limit is checked.
//
// Note: by avoiding a dust limit check you may end up with a transaction that is non-standard.
func (_self *TxBuilder) AllowDust(allowDust bool) *TxBuilder {
	_pointer := _self.ffiObject.incrementPointer("*TxBuilder")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterTxBuilderINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) unsafe.Pointer {
		return C.uniffi_bdkffi_fn_method_txbuilder_allow_dust(
			_pointer, FfiConverterBoolINSTANCE.Lower(allowDust), _uniffiStatus)
	}))
}

// Set a specific `ChangeSpendPolicy`. See `TxBuilder::do_not_spend_change` and `TxBuilder::only_spend_change` for
// some shortcuts. This method assumes the presence of an internal keychain, otherwise it has no effect.
func (_self *TxBuilder) ChangePolicy(changePolicy ChangeSpendPolicy) *TxBuilder {
	_pointer := _self.ffiObject.incrementPointer("*TxBuilder")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterTxBuilderINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) unsafe.Pointer {
		return C.uniffi_bdkffi_fn_method_txbuilder_change_policy(
			_pointer, FfiConverterChangeSpendPolicyINSTANCE.Lower(changePolicy), _uniffiStatus)
	}))
}

// Set the current blockchain height.
//
// This will be used to:
//
// 1. Set the `nLockTime` for preventing fee sniping. Note: This will be ignored if you manually specify a
// `nlocktime` using `TxBuilder::nlocktime`.
//
// 2. Decide whether coinbase outputs are mature or not. If the coinbase outputs are not mature at `current_height`,
// we ignore them in the coin selection. If you want to create a transaction that spends immature coinbase inputs,
// manually add them using `TxBuilder::add_utxos`.
// In both cases, if you don’t provide a current height, we use the last sync height.
func (_self *TxBuilder) CurrentHeight(height uint32) *TxBuilder {
	_pointer := _self.ffiObject.incrementPointer("*TxBuilder")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterTxBuilderINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) unsafe.Pointer {
		return C.uniffi_bdkffi_fn_method_txbuilder_current_height(
			_pointer, FfiConverterUint32INSTANCE.Lower(height), _uniffiStatus)
	}))
}

// Do not spend change outputs.
//
// This effectively adds all the change outputs to the "unspendable" list. See `TxBuilder::unspendable`. This method
// assumes the presence of an internal keychain, otherwise it has no effect.
func (_self *TxBuilder) DoNotSpendChange() *TxBuilder {
	_pointer := _self.ffiObject.incrementPointer("*TxBuilder")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterTxBuilderINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) unsafe.Pointer {
		return C.uniffi_bdkffi_fn_method_txbuilder_do_not_spend_change(
			_pointer, _uniffiStatus)
	}))
}

// Sets the address to drain excess coins to.
//
// Usually, when there are excess coins they are sent to a change address generated by the wallet. This option
// replaces the usual change address with an arbitrary script_pubkey of your choosing. Just as with a change output,
// if the drain output is not needed (the excess coins are too small) it will not be included in the resulting
// transaction. The only difference is that it is valid to use `drain_to` without setting any ordinary recipients
// with `add_recipient` (but it is perfectly fine to add recipients as well).
//
// If you choose not to set any recipients, you should provide the utxos that the transaction should spend via
// `add_utxos`. `drain_to` is very useful for draining all the coins in a wallet with `drain_wallet` to a single
// address.
func (_self *TxBuilder) DrainTo(script *Script) *TxBuilder {
	_pointer := _self.ffiObject.incrementPointer("*TxBuilder")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterTxBuilderINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) unsafe.Pointer {
		return C.uniffi_bdkffi_fn_method_txbuilder_drain_to(
			_pointer, FfiConverterScriptINSTANCE.Lower(script), _uniffiStatus)
	}))
}

// Spend all the available inputs. This respects filters like `TxBuilder::unspendable` and the change policy.
func (_self *TxBuilder) DrainWallet() *TxBuilder {
	_pointer := _self.ffiObject.incrementPointer("*TxBuilder")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterTxBuilderINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) unsafe.Pointer {
		return C.uniffi_bdkffi_fn_method_txbuilder_drain_wallet(
			_pointer, _uniffiStatus)
	}))
}

// Set an absolute fee The `fee_absolute` method refers to the absolute transaction fee in `Amount`. If anyone sets
// both the `fee_absolute` method and the `fee_rate` method, the `FeePolicy` enum will be set by whichever method was
// called last, as the `FeeRate` and `FeeAmount` are mutually exclusive.
//
// Note that this is really a minimum absolute fee – it’s possible to overshoot it slightly since adding a change output to drain the remaining excess might not be viable.
func (_self *TxBuilder) FeeAbsolute(fee *Amount) *TxBuilder {
	_pointer := _self.ffiObject.incrementPointer("*TxBuilder")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterTxBuilderINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) unsafe.Pointer {
		return C.uniffi_bdkffi_fn_method_txbuilder_fee_absolute(
			_pointer, FfiConverterAmountINSTANCE.Lower(fee), _uniffiStatus)
	}))
}

// Set a custom fee rate.
//
// This method sets the mining fee paid by the transaction as a rate on its size. This means that the total fee paid
// is equal to fee_rate times the size of the transaction. Default is 1 sat/vB in accordance with Bitcoin Core’s
// default relay policy.
//
// Note that this is really a minimum feerate – it’s possible to overshoot it slightly since adding a change output
// to drain the remaining excess might not be viable.
func (_self *TxBuilder) FeeRate(feeRate *FeeRate) *TxBuilder {
	_pointer := _self.ffiObject.incrementPointer("*TxBuilder")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterTxBuilderINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) unsafe.Pointer {
		return C.uniffi_bdkffi_fn_method_txbuilder_fee_rate(
			_pointer, FfiConverterFeeRateINSTANCE.Lower(feeRate), _uniffiStatus)
	}))
}

// Finish building the transaction.
//
// Uses the thread-local random number generator (rng).
//
// Returns a new `Psbt` per BIP174.
//
// WARNING: To avoid change address reuse you must persist the changes resulting from one or more calls to this
// method before closing the wallet. See `Wallet::reveal_next_address`.
func (_self *TxBuilder) Finish(wallet *Wallet) (*Psbt, error) {
	_pointer := _self.ffiObject.incrementPointer("*TxBuilder")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[CreateTxError](FfiConverterCreateTxError{}, func(_uniffiStatus *C.RustCallStatus) unsafe.Pointer {
		return C.uniffi_bdkffi_fn_method_txbuilder_finish(
			_pointer, FfiConverterWalletINSTANCE.Lower(wallet), _uniffiStatus)
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue *Psbt
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterPsbtINSTANCE.Lift(_uniffiRV), nil
	}
}

// Only spend utxos added by `TxBuilder::add_utxo`.
//
// The wallet will not add additional utxos to the transaction even if they are needed to make the transaction valid.
func (_self *TxBuilder) ManuallySelectedOnly() *TxBuilder {
	_pointer := _self.ffiObject.incrementPointer("*TxBuilder")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterTxBuilderINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) unsafe.Pointer {
		return C.uniffi_bdkffi_fn_method_txbuilder_manually_selected_only(
			_pointer, _uniffiStatus)
	}))
}

// Use a specific nLockTime while creating the transaction.
//
// This can cause conflicts if the wallet’s descriptors contain an "after" (`OP_CLTV`) operator.
func (_self *TxBuilder) Nlocktime(locktime LockTime) *TxBuilder {
	_pointer := _self.ffiObject.incrementPointer("*TxBuilder")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterTxBuilderINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) unsafe.Pointer {
		return C.uniffi_bdkffi_fn_method_txbuilder_nlocktime(
			_pointer, FfiConverterLockTimeINSTANCE.Lower(locktime), _uniffiStatus)
	}))
}

// Only spend change outputs.
//
// This effectively adds all the non-change outputs to the "unspendable" list. See `TxBuilder::unspendable`. This
// method assumes the presence of an internal keychain, otherwise it has no effect.
func (_self *TxBuilder) OnlySpendChange() *TxBuilder {
	_pointer := _self.ffiObject.incrementPointer("*TxBuilder")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterTxBuilderINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) unsafe.Pointer {
		return C.uniffi_bdkffi_fn_method_txbuilder_only_spend_change(
			_pointer, _uniffiStatus)
	}))
}

// The TxBuilder::policy_path is a complex API. See the Rust docs for complete information: https://docs.rs/bdk_wallet/latest/bdk_wallet/struct.TxBuilder.html#method.policy_path
func (_self *TxBuilder) PolicyPath(policyPath map[string][]uint64, keychain KeychainKind) *TxBuilder {
	_pointer := _self.ffiObject.incrementPointer("*TxBuilder")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterTxBuilderINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) unsafe.Pointer {
		return C.uniffi_bdkffi_fn_method_txbuilder_policy_path(
			_pointer, FfiConverterMapStringSequenceUint64INSTANCE.Lower(policyPath), FfiConverterKeychainKindINSTANCE.Lower(keychain), _uniffiStatus)
	}))
}

// Set an exact `nSequence` value.
//
// This can cause conflicts if the wallet’s descriptors contain an "older" (`OP_CSV`) operator and the given
// `nsequence` is lower than the CSV value.
func (_self *TxBuilder) SetExactSequence(nsequence uint32) *TxBuilder {
	_pointer := _self.ffiObject.incrementPointer("*TxBuilder")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterTxBuilderINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) unsafe.Pointer {
		return C.uniffi_bdkffi_fn_method_txbuilder_set_exact_sequence(
			_pointer, FfiConverterUint32INSTANCE.Lower(nsequence), _uniffiStatus)
	}))
}

// Replace the recipients already added with a new list of recipients.
func (_self *TxBuilder) SetRecipients(recipients []ScriptAmount) *TxBuilder {
	_pointer := _self.ffiObject.incrementPointer("*TxBuilder")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterTxBuilderINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) unsafe.Pointer {
		return C.uniffi_bdkffi_fn_method_txbuilder_set_recipients(
			_pointer, FfiConverterSequenceScriptAmountINSTANCE.Lower(recipients), _uniffiStatus)
	}))
}

// Replace the internal list of unspendable utxos with a new list.
//
// It’s important to note that the "must-be-spent" utxos added with `TxBuilder::add_utxo` have priority over these.
func (_self *TxBuilder) Unspendable(unspendable []OutPoint) *TxBuilder {
	_pointer := _self.ffiObject.incrementPointer("*TxBuilder")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterTxBuilderINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) unsafe.Pointer {
		return C.uniffi_bdkffi_fn_method_txbuilder_unspendable(
			_pointer, FfiConverterSequenceOutPointINSTANCE.Lower(unspendable), _uniffiStatus)
	}))
}

// Build a transaction with a specific version.
//
// The version should always be greater than 0 and greater than 1 if the wallet’s descriptors contain an "older"
// (`OP_CSV`) operator.
func (_self *TxBuilder) Version(version int32) *TxBuilder {
	_pointer := _self.ffiObject.incrementPointer("*TxBuilder")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterTxBuilderINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) unsafe.Pointer {
		return C.uniffi_bdkffi_fn_method_txbuilder_version(
			_pointer, FfiConverterInt32INSTANCE.Lower(version), _uniffiStatus)
	}))
}
func (object *TxBuilder) Destroy() {
	runtime.SetFinalizer(object, nil)
	object.ffiObject.destroy()
}

type FfiConverterTxBuilder struct{}

var FfiConverterTxBuilderINSTANCE = FfiConverterTxBuilder{}

func (c FfiConverterTxBuilder) Lift(pointer unsafe.Pointer) *TxBuilder {
	result := &TxBuilder{
		newFfiObject(
			pointer,
			func(pointer unsafe.Pointer, status *C.RustCallStatus) unsafe.Pointer {
				return C.uniffi_bdkffi_fn_clone_txbuilder(pointer, status)
			},
			func(pointer unsafe.Pointer, status *C.RustCallStatus) {
				C.uniffi_bdkffi_fn_free_txbuilder(pointer, status)
			},
		),
	}
	runtime.SetFinalizer(result, (*TxBuilder).Destroy)
	return result
}

func (c FfiConverterTxBuilder) Read(reader io.Reader) *TxBuilder {
	return c.Lift(unsafe.Pointer(uintptr(readUint64(reader))))
}

func (c FfiConverterTxBuilder) Lower(value *TxBuilder) unsafe.Pointer {
	// TODO: this is bad - all synchronization from ObjectRuntime.go is discarded here,
	// because the pointer will be decremented immediately after this function returns,
	// and someone will be left holding onto a non-locked pointer.
	pointer := value.ffiObject.incrementPointer("*TxBuilder")
	defer value.ffiObject.decrementPointer()
	return pointer

}

func (c FfiConverterTxBuilder) Write(writer io.Writer, value *TxBuilder) {
	writeUint64(writer, uint64(uintptr(c.Lower(value))))
}

type FfiDestroyerTxBuilder struct{}

func (_ FfiDestroyerTxBuilder) Destroy(value *TxBuilder) {
	value.Destroy()
}

type UpdateInterface interface {
}
type Update struct {
	ffiObject FfiObject
}

func (object *Update) Destroy() {
	runtime.SetFinalizer(object, nil)
	object.ffiObject.destroy()
}

type FfiConverterUpdate struct{}

var FfiConverterUpdateINSTANCE = FfiConverterUpdate{}

func (c FfiConverterUpdate) Lift(pointer unsafe.Pointer) *Update {
	result := &Update{
		newFfiObject(
			pointer,
			func(pointer unsafe.Pointer, status *C.RustCallStatus) unsafe.Pointer {
				return C.uniffi_bdkffi_fn_clone_update(pointer, status)
			},
			func(pointer unsafe.Pointer, status *C.RustCallStatus) {
				C.uniffi_bdkffi_fn_free_update(pointer, status)
			},
		),
	}
	runtime.SetFinalizer(result, (*Update).Destroy)
	return result
}

func (c FfiConverterUpdate) Read(reader io.Reader) *Update {
	return c.Lift(unsafe.Pointer(uintptr(readUint64(reader))))
}

func (c FfiConverterUpdate) Lower(value *Update) unsafe.Pointer {
	// TODO: this is bad - all synchronization from ObjectRuntime.go is discarded here,
	// because the pointer will be decremented immediately after this function returns,
	// and someone will be left holding onto a non-locked pointer.
	pointer := value.ffiObject.incrementPointer("*Update")
	defer value.ffiObject.decrementPointer()
	return pointer

}

func (c FfiConverterUpdate) Write(writer io.Writer, value *Update) {
	writeUint64(writer, uint64(uintptr(c.Lower(value))))
}

type FfiDestroyerUpdate struct{}

func (_ FfiDestroyerUpdate) Destroy(value *Update) {
	value.Destroy()
}

type WalletInterface interface {
	// Applies an update to the wallet and stages the changes (but does not persist them).
	//
	// Usually you create an `update` by interacting with some blockchain data source and inserting
	// transactions related to your wallet into it.
	//
	// After applying updates you should persist the staged wallet changes. For an example of how
	// to persist staged wallet changes see [`Wallet::reveal_next_address`].
	ApplyUpdate(update *Update) error
	// Return the balance, separated into available, trusted-pending, untrusted-pending and immature
	// values.
	Balance() Balance
	// Calculates the fee of a given transaction. Returns [`Amount::ZERO`] if `tx` is a coinbase transaction.
	//
	// To calculate the fee for a [`Transaction`] with inputs not owned by this wallet you must
	// manually insert the TxOut(s) into the tx graph using the [`insert_txout`] function.
	//
	// Note `tx` does not have to be in the graph for this to work.
	CalculateFee(tx *Transaction) (*Amount, error)
	// Calculate the [`FeeRate`] for a given transaction.
	//
	// To calculate the fee rate for a [`Transaction`] with inputs not owned by this wallet you must
	// manually insert the TxOut(s) into the tx graph using the [`insert_txout`] function.
	//
	// Note `tx` does not have to be in the graph for this to work.
	CalculateFeeRate(tx *Transaction) (*FeeRate, error)
	// Informs the wallet that you no longer intend to broadcast a tx that was built from it.
	//
	// This frees up the change address used when creating the tx for use in future transactions.
	CancelTx(tx *Transaction)
	// The derivation index of this wallet. It will return `None` if it has not derived any addresses.
	// Otherwise, it will return the index of the highest address it has derived.
	DerivationIndex(keychain KeychainKind) *uint32
	// Finds how the wallet derived the script pubkey `spk`.
	//
	// Will only return `Some(_)` if the wallet has given out the spk.
	DerivationOfSpk(spk *Script) *KeychainAndIndex
	// Return the checksum of the public descriptor associated to `keychain`
	//
	// Internally calls [`Self::public_descriptor`] to fetch the right descriptor
	DescriptorChecksum(keychain KeychainKind) string
	// Finalize a PSBT, i.e., for each input determine if sufficient data is available to pass
	// validation and construct the respective `scriptSig` or `scriptWitness`. Please refer to
	// [BIP174](https://github.com/bitcoin/bips/blob/master/bip-0174.mediawiki#Input_Finalizer),
	// and [BIP371](https://github.com/bitcoin/bips/blob/master/bip-0371.mediawiki)
	// for further information.
	//
	// Returns `true` if the PSBT could be finalized, and `false` otherwise.
	//
	// The [`SignOptions`] can be used to tweak the behavior of the finalizer.
	FinalizePsbt(psbt *Psbt, signOptions *SignOptions) (bool, error)
	// Get a single transaction from the wallet as a [`WalletTx`] (if the transaction exists).
	//
	// `WalletTx` contains the full transaction alongside meta-data such as:
	// * Blocks that the transaction is [`Anchor`]ed in. These may or may not be blocks that exist
	//   in the best chain.
	// * The [`ChainPosition`] of the transaction in the best chain - whether the transaction is
	//   confirmed or unconfirmed. If the transaction is confirmed, the anchor which proves the
	//   confirmation is provided. If the transaction is unconfirmed, the unix timestamp of when
	//   the transaction was last seen in the mempool is provided.
	GetTx(txid string) (*CanonicalTx, error)
	// Returns the utxo owned by this wallet corresponding to `outpoint` if it exists in the
	// wallet's database.
	GetUtxo(op OutPoint) *LocalOutput
	// Return whether or not a `script` is part of this wallet (either internal or external)
	IsMine(script *Script) bool
	// List all relevant outputs (includes both spent and unspent, confirmed and unconfirmed).
	//
	// To list only unspent outputs (UTXOs), use [`Wallet::list_unspent`] instead.
	ListOutput() []LocalOutput
	// Return the list of unspent outputs of this wallet
	ListUnspent() []LocalOutput
	// List addresses that are revealed but unused.
	//
	// Note if the returned iterator is empty you can reveal more addresses
	// by using [`reveal_next_address`](Self::reveal_next_address) or
	// [`reveal_addresses_to`](Self::reveal_addresses_to).
	ListUnusedAddresses(keychain KeychainKind) []AddressInfo
	// Marks an address used of the given `keychain` at `index`.
	//
	// Returns whether the given index was present and then removed from the unused set.
	MarkUsed(keychain KeychainKind, index uint32) bool
	// Get the Bitcoin network the wallet is using.
	Network() Network
	// The index of the next address that you would get if you were to ask the wallet for a new address
	NextDerivationIndex(keychain KeychainKind) uint32
	// Get the next unused address for the given `keychain`, i.e. the address with the lowest
	// derivation index that hasn't been used in a transaction.
	//
	// This will attempt to reveal a new address if all previously revealed addresses have
	// been used, in which case the returned address will be the same as calling [`Wallet::reveal_next_address`].
	//
	// **WARNING**: To avoid address reuse you must persist the changes resulting from one or more
	// calls to this method before closing the wallet. See [`Wallet::reveal_next_address`].
	NextUnusedAddress(keychain KeychainKind) AddressInfo
	// Peek an address of the given `keychain` at `index` without revealing it.
	//
	// For non-wildcard descriptors this returns the same address at every provided index.
	//
	// # Panics
	//
	// This panics when the caller requests for an address of derivation index greater than the
	// [BIP32](https://github.com/bitcoin/bips/blob/master/bip-0032.mediawiki) max index.
	PeekAddress(keychain KeychainKind, index uint32) AddressInfo
	Persist(connection *Connection) (bool, error)
	Policies(keychain KeychainKind) (**Policy, error)
	// Reveal addresses up to and including the target `index` and return an iterator
	// of newly revealed addresses.
	//
	// If the target `index` is unreachable, we make a best effort to reveal up to the last
	// possible index. If all addresses up to the given `index` are already revealed, then
	// no new addresses are returned.
	//
	// **WARNING**: To avoid address reuse you must persist the changes resulting from one or more
	// calls to this method before closing the wallet. See [`Wallet::reveal_next_address`].
	RevealAddressesTo(keychain KeychainKind, index uint32) []AddressInfo
	// Attempt to reveal the next address of the given `keychain`.
	//
	// This will increment the keychain's derivation index. If the keychain's descriptor doesn't
	// contain a wildcard or every address is already revealed up to the maximum derivation
	// index defined in [BIP32](https://github.com/bitcoin/bips/blob/master/bip-0032.mediawiki),
	// then the last revealed address will be returned.
	RevealNextAddress(keychain KeychainKind) AddressInfo
	// Compute the `tx`'s sent and received [`Amount`]s.
	//
	// This method returns a tuple `(sent, received)`. Sent is the sum of the txin amounts
	// that spend from previous txouts tracked by this wallet. Received is the summation
	// of this tx's outputs that send to script pubkeys tracked by this wallet.
	SentAndReceived(tx *Transaction) SentAndReceivedValues
	// Sign a transaction with all the wallet's signers, in the order specified by every signer's
	// [`SignerOrdering`]. This function returns the `Result` type with an encapsulated `bool` that has the value true if the PSBT was finalized, or false otherwise.
	//
	// The [`SignOptions`] can be used to tweak the behavior of the software signers, and the way
	// the transaction is finalized at the end. Note that it can't be guaranteed that *every*
	// signers will follow the options, but the "software signers" (WIF keys and `xprv`) defined
	// in this library will.
	Sign(psbt *Psbt, signOptions *SignOptions) (bool, error)
	// Create a [`FullScanRequest] for this wallet.
	//
	// This is the first step when performing a spk-based wallet full scan, the returned
	// [`FullScanRequest] collects iterators for the wallet's keychain script pub keys needed to
	// start a blockchain full scan with a spk based blockchain client.
	//
	// This operation is generally only used when importing or restoring a previously used wallet
	// in which the list of used scripts is not known.
	StartFullScan() *FullScanRequestBuilder
	// Create a partial [`SyncRequest`] for this wallet for all revealed spks.
	//
	// This is the first step when performing a spk-based wallet partial sync, the returned
	// [`SyncRequest`] collects all revealed script pubkeys from the wallet keychain needed to
	// start a blockchain sync with a spk based blockchain client.
	StartSyncWithRevealedSpks() *SyncRequestBuilder
	// Iterate over the transactions in the wallet.
	Transactions() []CanonicalTx
}
type Wallet struct {
	ffiObject FfiObject
}

func NewWallet(descriptor *Descriptor, changeDescriptor *Descriptor, network Network, connection *Connection) (*Wallet, error) {
	_uniffiRV, _uniffiErr := rustCallWithError[CreateWithPersistError](FfiConverterCreateWithPersistError{}, func(_uniffiStatus *C.RustCallStatus) unsafe.Pointer {
		return C.uniffi_bdkffi_fn_constructor_wallet_new(FfiConverterDescriptorINSTANCE.Lower(descriptor), FfiConverterDescriptorINSTANCE.Lower(changeDescriptor), FfiConverterNetworkINSTANCE.Lower(network), FfiConverterConnectionINSTANCE.Lower(connection), _uniffiStatus)
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue *Wallet
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterWalletINSTANCE.Lift(_uniffiRV), nil
	}
}

func WalletLoad(descriptor *Descriptor, changeDescriptor *Descriptor, connection *Connection) (*Wallet, error) {
	_uniffiRV, _uniffiErr := rustCallWithError[LoadWithPersistError](FfiConverterLoadWithPersistError{}, func(_uniffiStatus *C.RustCallStatus) unsafe.Pointer {
		return C.uniffi_bdkffi_fn_constructor_wallet_load(FfiConverterDescriptorINSTANCE.Lower(descriptor), FfiConverterDescriptorINSTANCE.Lower(changeDescriptor), FfiConverterConnectionINSTANCE.Lower(connection), _uniffiStatus)
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue *Wallet
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterWalletINSTANCE.Lift(_uniffiRV), nil
	}
}

// Applies an update to the wallet and stages the changes (but does not persist them).
//
// Usually you create an `update` by interacting with some blockchain data source and inserting
// transactions related to your wallet into it.
//
// After applying updates you should persist the staged wallet changes. For an example of how
// to persist staged wallet changes see [`Wallet::reveal_next_address`].
func (_self *Wallet) ApplyUpdate(update *Update) error {
	_pointer := _self.ffiObject.incrementPointer("*Wallet")
	defer _self.ffiObject.decrementPointer()
	_, _uniffiErr := rustCallWithError[CannotConnectError](FfiConverterCannotConnectError{}, func(_uniffiStatus *C.RustCallStatus) bool {
		C.uniffi_bdkffi_fn_method_wallet_apply_update(
			_pointer, FfiConverterUpdateINSTANCE.Lower(update), _uniffiStatus)
		return false
	})
	return _uniffiErr.AsError()
}

// Return the balance, separated into available, trusted-pending, untrusted-pending and immature
// values.
func (_self *Wallet) Balance() Balance {
	_pointer := _self.ffiObject.incrementPointer("*Wallet")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterBalanceINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_bdkffi_fn_method_wallet_balance(
				_pointer, _uniffiStatus),
		}
	}))
}

// Calculates the fee of a given transaction. Returns [`Amount::ZERO`] if `tx` is a coinbase transaction.
//
// To calculate the fee for a [`Transaction`] with inputs not owned by this wallet you must
// manually insert the TxOut(s) into the tx graph using the [`insert_txout`] function.
//
// Note `tx` does not have to be in the graph for this to work.
func (_self *Wallet) CalculateFee(tx *Transaction) (*Amount, error) {
	_pointer := _self.ffiObject.incrementPointer("*Wallet")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[CalculateFeeError](FfiConverterCalculateFeeError{}, func(_uniffiStatus *C.RustCallStatus) unsafe.Pointer {
		return C.uniffi_bdkffi_fn_method_wallet_calculate_fee(
			_pointer, FfiConverterTransactionINSTANCE.Lower(tx), _uniffiStatus)
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue *Amount
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterAmountINSTANCE.Lift(_uniffiRV), nil
	}
}

// Calculate the [`FeeRate`] for a given transaction.
//
// To calculate the fee rate for a [`Transaction`] with inputs not owned by this wallet you must
// manually insert the TxOut(s) into the tx graph using the [`insert_txout`] function.
//
// Note `tx` does not have to be in the graph for this to work.
func (_self *Wallet) CalculateFeeRate(tx *Transaction) (*FeeRate, error) {
	_pointer := _self.ffiObject.incrementPointer("*Wallet")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[CalculateFeeError](FfiConverterCalculateFeeError{}, func(_uniffiStatus *C.RustCallStatus) unsafe.Pointer {
		return C.uniffi_bdkffi_fn_method_wallet_calculate_fee_rate(
			_pointer, FfiConverterTransactionINSTANCE.Lower(tx), _uniffiStatus)
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue *FeeRate
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterFeeRateINSTANCE.Lift(_uniffiRV), nil
	}
}

// Informs the wallet that you no longer intend to broadcast a tx that was built from it.
//
// This frees up the change address used when creating the tx for use in future transactions.
func (_self *Wallet) CancelTx(tx *Transaction) {
	_pointer := _self.ffiObject.incrementPointer("*Wallet")
	defer _self.ffiObject.decrementPointer()
	rustCall(func(_uniffiStatus *C.RustCallStatus) bool {
		C.uniffi_bdkffi_fn_method_wallet_cancel_tx(
			_pointer, FfiConverterTransactionINSTANCE.Lower(tx), _uniffiStatus)
		return false
	})
}

// The derivation index of this wallet. It will return `None` if it has not derived any addresses.
// Otherwise, it will return the index of the highest address it has derived.
func (_self *Wallet) DerivationIndex(keychain KeychainKind) *uint32 {
	_pointer := _self.ffiObject.incrementPointer("*Wallet")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterOptionalUint32INSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_bdkffi_fn_method_wallet_derivation_index(
				_pointer, FfiConverterKeychainKindINSTANCE.Lower(keychain), _uniffiStatus),
		}
	}))
}

// Finds how the wallet derived the script pubkey `spk`.
//
// Will only return `Some(_)` if the wallet has given out the spk.
func (_self *Wallet) DerivationOfSpk(spk *Script) *KeychainAndIndex {
	_pointer := _self.ffiObject.incrementPointer("*Wallet")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterOptionalKeychainAndIndexINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_bdkffi_fn_method_wallet_derivation_of_spk(
				_pointer, FfiConverterScriptINSTANCE.Lower(spk), _uniffiStatus),
		}
	}))
}

// Return the checksum of the public descriptor associated to `keychain`
//
// Internally calls [`Self::public_descriptor`] to fetch the right descriptor
func (_self *Wallet) DescriptorChecksum(keychain KeychainKind) string {
	_pointer := _self.ffiObject.incrementPointer("*Wallet")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterStringINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_bdkffi_fn_method_wallet_descriptor_checksum(
				_pointer, FfiConverterKeychainKindINSTANCE.Lower(keychain), _uniffiStatus),
		}
	}))
}

// Finalize a PSBT, i.e., for each input determine if sufficient data is available to pass
// validation and construct the respective `scriptSig` or `scriptWitness`. Please refer to
// [BIP174](https://github.com/bitcoin/bips/blob/master/bip-0174.mediawiki#Input_Finalizer),
// and [BIP371](https://github.com/bitcoin/bips/blob/master/bip-0371.mediawiki)
// for further information.
//
// Returns `true` if the PSBT could be finalized, and `false` otherwise.
//
// The [`SignOptions`] can be used to tweak the behavior of the finalizer.
func (_self *Wallet) FinalizePsbt(psbt *Psbt, signOptions *SignOptions) (bool, error) {
	_pointer := _self.ffiObject.incrementPointer("*Wallet")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[SignerError](FfiConverterSignerError{}, func(_uniffiStatus *C.RustCallStatus) C.int8_t {
		return C.uniffi_bdkffi_fn_method_wallet_finalize_psbt(
			_pointer, FfiConverterPsbtINSTANCE.Lower(psbt), FfiConverterOptionalSignOptionsINSTANCE.Lower(signOptions), _uniffiStatus)
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue bool
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterBoolINSTANCE.Lift(_uniffiRV), nil
	}
}

// Get a single transaction from the wallet as a [`WalletTx`] (if the transaction exists).
//
// `WalletTx` contains the full transaction alongside meta-data such as:
//   - Blocks that the transaction is [`Anchor`]ed in. These may or may not be blocks that exist
//     in the best chain.
//   - The [`ChainPosition`] of the transaction in the best chain - whether the transaction is
//     confirmed or unconfirmed. If the transaction is confirmed, the anchor which proves the
//     confirmation is provided. If the transaction is unconfirmed, the unix timestamp of when
//     the transaction was last seen in the mempool is provided.
func (_self *Wallet) GetTx(txid string) (*CanonicalTx, error) {
	_pointer := _self.ffiObject.incrementPointer("*Wallet")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[TxidParseError](FfiConverterTxidParseError{}, func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_bdkffi_fn_method_wallet_get_tx(
				_pointer, FfiConverterStringINSTANCE.Lower(txid), _uniffiStatus),
		}
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue *CanonicalTx
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterOptionalCanonicalTxINSTANCE.Lift(_uniffiRV), nil
	}
}

// Returns the utxo owned by this wallet corresponding to `outpoint` if it exists in the
// wallet's database.
func (_self *Wallet) GetUtxo(op OutPoint) *LocalOutput {
	_pointer := _self.ffiObject.incrementPointer("*Wallet")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterOptionalLocalOutputINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_bdkffi_fn_method_wallet_get_utxo(
				_pointer, FfiConverterOutPointINSTANCE.Lower(op), _uniffiStatus),
		}
	}))
}

// Return whether or not a `script` is part of this wallet (either internal or external)
func (_self *Wallet) IsMine(script *Script) bool {
	_pointer := _self.ffiObject.incrementPointer("*Wallet")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterBoolINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) C.int8_t {
		return C.uniffi_bdkffi_fn_method_wallet_is_mine(
			_pointer, FfiConverterScriptINSTANCE.Lower(script), _uniffiStatus)
	}))
}

// List all relevant outputs (includes both spent and unspent, confirmed and unconfirmed).
//
// To list only unspent outputs (UTXOs), use [`Wallet::list_unspent`] instead.
func (_self *Wallet) ListOutput() []LocalOutput {
	_pointer := _self.ffiObject.incrementPointer("*Wallet")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterSequenceLocalOutputINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_bdkffi_fn_method_wallet_list_output(
				_pointer, _uniffiStatus),
		}
	}))
}

// Return the list of unspent outputs of this wallet
func (_self *Wallet) ListUnspent() []LocalOutput {
	_pointer := _self.ffiObject.incrementPointer("*Wallet")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterSequenceLocalOutputINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_bdkffi_fn_method_wallet_list_unspent(
				_pointer, _uniffiStatus),
		}
	}))
}

// List addresses that are revealed but unused.
//
// Note if the returned iterator is empty you can reveal more addresses
// by using [`reveal_next_address`](Self::reveal_next_address) or
// [`reveal_addresses_to`](Self::reveal_addresses_to).
func (_self *Wallet) ListUnusedAddresses(keychain KeychainKind) []AddressInfo {
	_pointer := _self.ffiObject.incrementPointer("*Wallet")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterSequenceAddressInfoINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_bdkffi_fn_method_wallet_list_unused_addresses(
				_pointer, FfiConverterKeychainKindINSTANCE.Lower(keychain), _uniffiStatus),
		}
	}))
}

// Marks an address used of the given `keychain` at `index`.
//
// Returns whether the given index was present and then removed from the unused set.
func (_self *Wallet) MarkUsed(keychain KeychainKind, index uint32) bool {
	_pointer := _self.ffiObject.incrementPointer("*Wallet")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterBoolINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) C.int8_t {
		return C.uniffi_bdkffi_fn_method_wallet_mark_used(
			_pointer, FfiConverterKeychainKindINSTANCE.Lower(keychain), FfiConverterUint32INSTANCE.Lower(index), _uniffiStatus)
	}))
}

// Get the Bitcoin network the wallet is using.
func (_self *Wallet) Network() Network {
	_pointer := _self.ffiObject.incrementPointer("*Wallet")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterNetworkINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_bdkffi_fn_method_wallet_network(
				_pointer, _uniffiStatus),
		}
	}))
}

// The index of the next address that you would get if you were to ask the wallet for a new address
func (_self *Wallet) NextDerivationIndex(keychain KeychainKind) uint32 {
	_pointer := _self.ffiObject.incrementPointer("*Wallet")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterUint32INSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint32_t {
		return C.uniffi_bdkffi_fn_method_wallet_next_derivation_index(
			_pointer, FfiConverterKeychainKindINSTANCE.Lower(keychain), _uniffiStatus)
	}))
}

// Get the next unused address for the given `keychain`, i.e. the address with the lowest
// derivation index that hasn't been used in a transaction.
//
// This will attempt to reveal a new address if all previously revealed addresses have
// been used, in which case the returned address will be the same as calling [`Wallet::reveal_next_address`].
//
// **WARNING**: To avoid address reuse you must persist the changes resulting from one or more
// calls to this method before closing the wallet. See [`Wallet::reveal_next_address`].
func (_self *Wallet) NextUnusedAddress(keychain KeychainKind) AddressInfo {
	_pointer := _self.ffiObject.incrementPointer("*Wallet")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterAddressInfoINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_bdkffi_fn_method_wallet_next_unused_address(
				_pointer, FfiConverterKeychainKindINSTANCE.Lower(keychain), _uniffiStatus),
		}
	}))
}

// Peek an address of the given `keychain` at `index` without revealing it.
//
// For non-wildcard descriptors this returns the same address at every provided index.
//
// # Panics
//
// This panics when the caller requests for an address of derivation index greater than the
// [BIP32](https://github.com/bitcoin/bips/blob/master/bip-0032.mediawiki) max index.
func (_self *Wallet) PeekAddress(keychain KeychainKind, index uint32) AddressInfo {
	_pointer := _self.ffiObject.incrementPointer("*Wallet")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterAddressInfoINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_bdkffi_fn_method_wallet_peek_address(
				_pointer, FfiConverterKeychainKindINSTANCE.Lower(keychain), FfiConverterUint32INSTANCE.Lower(index), _uniffiStatus),
		}
	}))
}

func (_self *Wallet) Persist(connection *Connection) (bool, error) {
	_pointer := _self.ffiObject.incrementPointer("*Wallet")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[SqliteError](FfiConverterSqliteError{}, func(_uniffiStatus *C.RustCallStatus) C.int8_t {
		return C.uniffi_bdkffi_fn_method_wallet_persist(
			_pointer, FfiConverterConnectionINSTANCE.Lower(connection), _uniffiStatus)
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue bool
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterBoolINSTANCE.Lift(_uniffiRV), nil
	}
}

func (_self *Wallet) Policies(keychain KeychainKind) (**Policy, error) {
	_pointer := _self.ffiObject.incrementPointer("*Wallet")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[DescriptorError](FfiConverterDescriptorError{}, func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_bdkffi_fn_method_wallet_policies(
				_pointer, FfiConverterKeychainKindINSTANCE.Lower(keychain), _uniffiStatus),
		}
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue **Policy
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterOptionalPolicyINSTANCE.Lift(_uniffiRV), nil
	}
}

// Reveal addresses up to and including the target `index` and return an iterator
// of newly revealed addresses.
//
// If the target `index` is unreachable, we make a best effort to reveal up to the last
// possible index. If all addresses up to the given `index` are already revealed, then
// no new addresses are returned.
//
// **WARNING**: To avoid address reuse you must persist the changes resulting from one or more
// calls to this method before closing the wallet. See [`Wallet::reveal_next_address`].
func (_self *Wallet) RevealAddressesTo(keychain KeychainKind, index uint32) []AddressInfo {
	_pointer := _self.ffiObject.incrementPointer("*Wallet")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterSequenceAddressInfoINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_bdkffi_fn_method_wallet_reveal_addresses_to(
				_pointer, FfiConverterKeychainKindINSTANCE.Lower(keychain), FfiConverterUint32INSTANCE.Lower(index), _uniffiStatus),
		}
	}))
}

// Attempt to reveal the next address of the given `keychain`.
//
// This will increment the keychain's derivation index. If the keychain's descriptor doesn't
// contain a wildcard or every address is already revealed up to the maximum derivation
// index defined in [BIP32](https://github.com/bitcoin/bips/blob/master/bip-0032.mediawiki),
// then the last revealed address will be returned.
func (_self *Wallet) RevealNextAddress(keychain KeychainKind) AddressInfo {
	_pointer := _self.ffiObject.incrementPointer("*Wallet")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterAddressInfoINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_bdkffi_fn_method_wallet_reveal_next_address(
				_pointer, FfiConverterKeychainKindINSTANCE.Lower(keychain), _uniffiStatus),
		}
	}))
}

// Compute the `tx`'s sent and received [`Amount`]s.
//
// This method returns a tuple `(sent, received)`. Sent is the sum of the txin amounts
// that spend from previous txouts tracked by this wallet. Received is the summation
// of this tx's outputs that send to script pubkeys tracked by this wallet.
func (_self *Wallet) SentAndReceived(tx *Transaction) SentAndReceivedValues {
	_pointer := _self.ffiObject.incrementPointer("*Wallet")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterSentAndReceivedValuesINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_bdkffi_fn_method_wallet_sent_and_received(
				_pointer, FfiConverterTransactionINSTANCE.Lower(tx), _uniffiStatus),
		}
	}))
}

// Sign a transaction with all the wallet's signers, in the order specified by every signer's
// [`SignerOrdering`]. This function returns the `Result` type with an encapsulated `bool` that has the value true if the PSBT was finalized, or false otherwise.
//
// The [`SignOptions`] can be used to tweak the behavior of the software signers, and the way
// the transaction is finalized at the end. Note that it can't be guaranteed that *every*
// signers will follow the options, but the "software signers" (WIF keys and `xprv`) defined
// in this library will.
func (_self *Wallet) Sign(psbt *Psbt, signOptions *SignOptions) (bool, error) {
	_pointer := _self.ffiObject.incrementPointer("*Wallet")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[SignerError](FfiConverterSignerError{}, func(_uniffiStatus *C.RustCallStatus) C.int8_t {
		return C.uniffi_bdkffi_fn_method_wallet_sign(
			_pointer, FfiConverterPsbtINSTANCE.Lower(psbt), FfiConverterOptionalSignOptionsINSTANCE.Lower(signOptions), _uniffiStatus)
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue bool
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterBoolINSTANCE.Lift(_uniffiRV), nil
	}
}

// Create a [`FullScanRequest] for this wallet.
//
// This is the first step when performing a spk-based wallet full scan, the returned
// [`FullScanRequest] collects iterators for the wallet's keychain script pub keys needed to
// start a blockchain full scan with a spk based blockchain client.
//
// This operation is generally only used when importing or restoring a previously used wallet
// in which the list of used scripts is not known.
func (_self *Wallet) StartFullScan() *FullScanRequestBuilder {
	_pointer := _self.ffiObject.incrementPointer("*Wallet")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterFullScanRequestBuilderINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) unsafe.Pointer {
		return C.uniffi_bdkffi_fn_method_wallet_start_full_scan(
			_pointer, _uniffiStatus)
	}))
}

// Create a partial [`SyncRequest`] for this wallet for all revealed spks.
//
// This is the first step when performing a spk-based wallet partial sync, the returned
// [`SyncRequest`] collects all revealed script pubkeys from the wallet keychain needed to
// start a blockchain sync with a spk based blockchain client.
func (_self *Wallet) StartSyncWithRevealedSpks() *SyncRequestBuilder {
	_pointer := _self.ffiObject.incrementPointer("*Wallet")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterSyncRequestBuilderINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) unsafe.Pointer {
		return C.uniffi_bdkffi_fn_method_wallet_start_sync_with_revealed_spks(
			_pointer, _uniffiStatus)
	}))
}

// Iterate over the transactions in the wallet.
func (_self *Wallet) Transactions() []CanonicalTx {
	_pointer := _self.ffiObject.incrementPointer("*Wallet")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterSequenceCanonicalTxINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_bdkffi_fn_method_wallet_transactions(
				_pointer, _uniffiStatus),
		}
	}))
}
func (object *Wallet) Destroy() {
	runtime.SetFinalizer(object, nil)
	object.ffiObject.destroy()
}

type FfiConverterWallet struct{}

var FfiConverterWalletINSTANCE = FfiConverterWallet{}

func (c FfiConverterWallet) Lift(pointer unsafe.Pointer) *Wallet {
	result := &Wallet{
		newFfiObject(
			pointer,
			func(pointer unsafe.Pointer, status *C.RustCallStatus) unsafe.Pointer {
				return C.uniffi_bdkffi_fn_clone_wallet(pointer, status)
			},
			func(pointer unsafe.Pointer, status *C.RustCallStatus) {
				C.uniffi_bdkffi_fn_free_wallet(pointer, status)
			},
		),
	}
	runtime.SetFinalizer(result, (*Wallet).Destroy)
	return result
}

func (c FfiConverterWallet) Read(reader io.Reader) *Wallet {
	return c.Lift(unsafe.Pointer(uintptr(readUint64(reader))))
}

func (c FfiConverterWallet) Lower(value *Wallet) unsafe.Pointer {
	// TODO: this is bad - all synchronization from ObjectRuntime.go is discarded here,
	// because the pointer will be decremented immediately after this function returns,
	// and someone will be left holding onto a non-locked pointer.
	pointer := value.ffiObject.incrementPointer("*Wallet")
	defer value.ffiObject.decrementPointer()
	return pointer

}

func (c FfiConverterWallet) Write(writer io.Writer, value *Wallet) {
	writeUint64(writer, uint64(uintptr(c.Lower(value))))
}

type FfiDestroyerWallet struct{}

func (_ FfiDestroyerWallet) Destroy(value *Wallet) {
	value.Destroy()
}

// A derived address and the index it was found at.
type AddressInfo struct {
	// Child index of this address
	Index uint32
	// Address
	Address *Address
	// Type of keychain
	Keychain KeychainKind
}

func (r *AddressInfo) Destroy() {
	FfiDestroyerUint32{}.Destroy(r.Index)
	FfiDestroyerAddress{}.Destroy(r.Address)
	FfiDestroyerKeychainKind{}.Destroy(r.Keychain)
}

type FfiConverterAddressInfo struct{}

var FfiConverterAddressInfoINSTANCE = FfiConverterAddressInfo{}

func (c FfiConverterAddressInfo) Lift(rb RustBufferI) AddressInfo {
	return LiftFromRustBuffer[AddressInfo](c, rb)
}

func (c FfiConverterAddressInfo) Read(reader io.Reader) AddressInfo {
	return AddressInfo{
		FfiConverterUint32INSTANCE.Read(reader),
		FfiConverterAddressINSTANCE.Read(reader),
		FfiConverterKeychainKindINSTANCE.Read(reader),
	}
}

func (c FfiConverterAddressInfo) Lower(value AddressInfo) C.RustBuffer {
	return LowerIntoRustBuffer[AddressInfo](c, value)
}

func (c FfiConverterAddressInfo) Write(writer io.Writer, value AddressInfo) {
	FfiConverterUint32INSTANCE.Write(writer, value.Index)
	FfiConverterAddressINSTANCE.Write(writer, value.Address)
	FfiConverterKeychainKindINSTANCE.Write(writer, value.Keychain)
}

type FfiDestroyerAddressInfo struct{}

func (_ FfiDestroyerAddressInfo) Destroy(value AddressInfo) {
	value.Destroy()
}

// Balance, differentiated into various categories.
type Balance struct {
	// All coinbase outputs not yet matured
	Immature *Amount
	// Unconfirmed UTXOs generated by a wallet tx
	TrustedPending *Amount
	// Unconfirmed UTXOs received from an external wallet
	UntrustedPending *Amount
	// Confirmed and immediately spendable balance
	Confirmed *Amount
	// Get sum of trusted_pending and confirmed coins.
	//
	// This is the balance you can spend right now that shouldn't get cancelled via another party
	// double spending it.
	TrustedSpendable *Amount
	// Get the whole balance visible to the wallet.
	Total *Amount
}

func (r *Balance) Destroy() {
	FfiDestroyerAmount{}.Destroy(r.Immature)
	FfiDestroyerAmount{}.Destroy(r.TrustedPending)
	FfiDestroyerAmount{}.Destroy(r.UntrustedPending)
	FfiDestroyerAmount{}.Destroy(r.Confirmed)
	FfiDestroyerAmount{}.Destroy(r.TrustedSpendable)
	FfiDestroyerAmount{}.Destroy(r.Total)
}

type FfiConverterBalance struct{}

var FfiConverterBalanceINSTANCE = FfiConverterBalance{}

func (c FfiConverterBalance) Lift(rb RustBufferI) Balance {
	return LiftFromRustBuffer[Balance](c, rb)
}

func (c FfiConverterBalance) Read(reader io.Reader) Balance {
	return Balance{
		FfiConverterAmountINSTANCE.Read(reader),
		FfiConverterAmountINSTANCE.Read(reader),
		FfiConverterAmountINSTANCE.Read(reader),
		FfiConverterAmountINSTANCE.Read(reader),
		FfiConverterAmountINSTANCE.Read(reader),
		FfiConverterAmountINSTANCE.Read(reader),
	}
}

func (c FfiConverterBalance) Lower(value Balance) C.RustBuffer {
	return LowerIntoRustBuffer[Balance](c, value)
}

func (c FfiConverterBalance) Write(writer io.Writer, value Balance) {
	FfiConverterAmountINSTANCE.Write(writer, value.Immature)
	FfiConverterAmountINSTANCE.Write(writer, value.TrustedPending)
	FfiConverterAmountINSTANCE.Write(writer, value.UntrustedPending)
	FfiConverterAmountINSTANCE.Write(writer, value.Confirmed)
	FfiConverterAmountINSTANCE.Write(writer, value.TrustedSpendable)
	FfiConverterAmountINSTANCE.Write(writer, value.Total)
}

type FfiDestroyerBalance struct{}

func (_ FfiDestroyerBalance) Destroy(value Balance) {
	value.Destroy()
}

type BlockId struct {
	Height uint32
	Hash   string
}

func (r *BlockId) Destroy() {
	FfiDestroyerUint32{}.Destroy(r.Height)
	FfiDestroyerString{}.Destroy(r.Hash)
}

type FfiConverterBlockId struct{}

var FfiConverterBlockIdINSTANCE = FfiConverterBlockId{}

func (c FfiConverterBlockId) Lift(rb RustBufferI) BlockId {
	return LiftFromRustBuffer[BlockId](c, rb)
}

func (c FfiConverterBlockId) Read(reader io.Reader) BlockId {
	return BlockId{
		FfiConverterUint32INSTANCE.Read(reader),
		FfiConverterStringINSTANCE.Read(reader),
	}
}

func (c FfiConverterBlockId) Lower(value BlockId) C.RustBuffer {
	return LowerIntoRustBuffer[BlockId](c, value)
}

func (c FfiConverterBlockId) Write(writer io.Writer, value BlockId) {
	FfiConverterUint32INSTANCE.Write(writer, value.Height)
	FfiConverterStringINSTANCE.Write(writer, value.Hash)
}

type FfiDestroyerBlockId struct{}

func (_ FfiDestroyerBlockId) Destroy(value BlockId) {
	value.Destroy()
}

type CanonicalTx struct {
	Transaction   *Transaction
	ChainPosition ChainPosition
}

func (r *CanonicalTx) Destroy() {
	FfiDestroyerTransaction{}.Destroy(r.Transaction)
	FfiDestroyerChainPosition{}.Destroy(r.ChainPosition)
}

type FfiConverterCanonicalTx struct{}

var FfiConverterCanonicalTxINSTANCE = FfiConverterCanonicalTx{}

func (c FfiConverterCanonicalTx) Lift(rb RustBufferI) CanonicalTx {
	return LiftFromRustBuffer[CanonicalTx](c, rb)
}

func (c FfiConverterCanonicalTx) Read(reader io.Reader) CanonicalTx {
	return CanonicalTx{
		FfiConverterTransactionINSTANCE.Read(reader),
		FfiConverterChainPositionINSTANCE.Read(reader),
	}
}

func (c FfiConverterCanonicalTx) Lower(value CanonicalTx) C.RustBuffer {
	return LowerIntoRustBuffer[CanonicalTx](c, value)
}

func (c FfiConverterCanonicalTx) Write(writer io.Writer, value CanonicalTx) {
	FfiConverterTransactionINSTANCE.Write(writer, value.Transaction)
	FfiConverterChainPositionINSTANCE.Write(writer, value.ChainPosition)
}

type FfiDestroyerCanonicalTx struct{}

func (_ FfiDestroyerCanonicalTx) Destroy(value CanonicalTx) {
	value.Destroy()
}

type Condition struct {
	Csv      *uint32
	Timelock *LockTime
}

func (r *Condition) Destroy() {
	FfiDestroyerOptionalUint32{}.Destroy(r.Csv)
	FfiDestroyerOptionalLockTime{}.Destroy(r.Timelock)
}

type FfiConverterCondition struct{}

var FfiConverterConditionINSTANCE = FfiConverterCondition{}

func (c FfiConverterCondition) Lift(rb RustBufferI) Condition {
	return LiftFromRustBuffer[Condition](c, rb)
}

func (c FfiConverterCondition) Read(reader io.Reader) Condition {
	return Condition{
		FfiConverterOptionalUint32INSTANCE.Read(reader),
		FfiConverterOptionalLockTimeINSTANCE.Read(reader),
	}
}

func (c FfiConverterCondition) Lower(value Condition) C.RustBuffer {
	return LowerIntoRustBuffer[Condition](c, value)
}

func (c FfiConverterCondition) Write(writer io.Writer, value Condition) {
	FfiConverterOptionalUint32INSTANCE.Write(writer, value.Csv)
	FfiConverterOptionalLockTimeINSTANCE.Write(writer, value.Timelock)
}

type FfiDestroyerCondition struct{}

func (_ FfiDestroyerCondition) Destroy(value Condition) {
	value.Destroy()
}

type ConfirmationBlockTime struct {
	BlockId          BlockId
	ConfirmationTime uint64
}

func (r *ConfirmationBlockTime) Destroy() {
	FfiDestroyerBlockId{}.Destroy(r.BlockId)
	FfiDestroyerUint64{}.Destroy(r.ConfirmationTime)
}

type FfiConverterConfirmationBlockTime struct{}

var FfiConverterConfirmationBlockTimeINSTANCE = FfiConverterConfirmationBlockTime{}

func (c FfiConverterConfirmationBlockTime) Lift(rb RustBufferI) ConfirmationBlockTime {
	return LiftFromRustBuffer[ConfirmationBlockTime](c, rb)
}

func (c FfiConverterConfirmationBlockTime) Read(reader io.Reader) ConfirmationBlockTime {
	return ConfirmationBlockTime{
		FfiConverterBlockIdINSTANCE.Read(reader),
		FfiConverterUint64INSTANCE.Read(reader),
	}
}

func (c FfiConverterConfirmationBlockTime) Lower(value ConfirmationBlockTime) C.RustBuffer {
	return LowerIntoRustBuffer[ConfirmationBlockTime](c, value)
}

func (c FfiConverterConfirmationBlockTime) Write(writer io.Writer, value ConfirmationBlockTime) {
	FfiConverterBlockIdINSTANCE.Write(writer, value.BlockId)
	FfiConverterUint64INSTANCE.Write(writer, value.ConfirmationTime)
}

type FfiDestroyerConfirmationBlockTime struct{}

func (_ FfiDestroyerConfirmationBlockTime) Destroy(value ConfirmationBlockTime) {
	value.Destroy()
}

type FinalizedPsbtResult struct {
	Psbt          *Psbt
	CouldFinalize bool
	Errors        *[]*PsbtFinalizeError
}

func (r *FinalizedPsbtResult) Destroy() {
	FfiDestroyerPsbt{}.Destroy(r.Psbt)
	FfiDestroyerBool{}.Destroy(r.CouldFinalize)
	FfiDestroyerOptionalSequencePsbtFinalizeError{}.Destroy(r.Errors)
}

type FfiConverterFinalizedPsbtResult struct{}

var FfiConverterFinalizedPsbtResultINSTANCE = FfiConverterFinalizedPsbtResult{}

func (c FfiConverterFinalizedPsbtResult) Lift(rb RustBufferI) FinalizedPsbtResult {
	return LiftFromRustBuffer[FinalizedPsbtResult](c, rb)
}

func (c FfiConverterFinalizedPsbtResult) Read(reader io.Reader) FinalizedPsbtResult {
	return FinalizedPsbtResult{
		FfiConverterPsbtINSTANCE.Read(reader),
		FfiConverterBoolINSTANCE.Read(reader),
		FfiConverterOptionalSequencePsbtFinalizeErrorINSTANCE.Read(reader),
	}
}

func (c FfiConverterFinalizedPsbtResult) Lower(value FinalizedPsbtResult) C.RustBuffer {
	return LowerIntoRustBuffer[FinalizedPsbtResult](c, value)
}

func (c FfiConverterFinalizedPsbtResult) Write(writer io.Writer, value FinalizedPsbtResult) {
	FfiConverterPsbtINSTANCE.Write(writer, value.Psbt)
	FfiConverterBoolINSTANCE.Write(writer, value.CouldFinalize)
	FfiConverterOptionalSequencePsbtFinalizeErrorINSTANCE.Write(writer, value.Errors)
}

type FfiDestroyerFinalizedPsbtResult struct{}

func (_ FfiDestroyerFinalizedPsbtResult) Destroy(value FinalizedPsbtResult) {
	value.Destroy()
}

type KeychainAndIndex struct {
	Keychain KeychainKind
	Index    uint32
}

func (r *KeychainAndIndex) Destroy() {
	FfiDestroyerKeychainKind{}.Destroy(r.Keychain)
	FfiDestroyerUint32{}.Destroy(r.Index)
}

type FfiConverterKeychainAndIndex struct{}

var FfiConverterKeychainAndIndexINSTANCE = FfiConverterKeychainAndIndex{}

func (c FfiConverterKeychainAndIndex) Lift(rb RustBufferI) KeychainAndIndex {
	return LiftFromRustBuffer[KeychainAndIndex](c, rb)
}

func (c FfiConverterKeychainAndIndex) Read(reader io.Reader) KeychainAndIndex {
	return KeychainAndIndex{
		FfiConverterKeychainKindINSTANCE.Read(reader),
		FfiConverterUint32INSTANCE.Read(reader),
	}
}

func (c FfiConverterKeychainAndIndex) Lower(value KeychainAndIndex) C.RustBuffer {
	return LowerIntoRustBuffer[KeychainAndIndex](c, value)
}

func (c FfiConverterKeychainAndIndex) Write(writer io.Writer, value KeychainAndIndex) {
	FfiConverterKeychainKindINSTANCE.Write(writer, value.Keychain)
	FfiConverterUint32INSTANCE.Write(writer, value.Index)
}

type FfiDestroyerKeychainAndIndex struct{}

func (_ FfiDestroyerKeychainAndIndex) Destroy(value KeychainAndIndex) {
	value.Destroy()
}

// An unspent output owned by a [`Wallet`].
type LocalOutput struct {
	// Reference to a transaction output
	Outpoint OutPoint
	// Transaction output
	Txout TxOut
	// Type of keychain
	Keychain KeychainKind
	// Whether this UTXO is spent or not
	IsSpent bool
	// The derivation index for the script pubkey in the wallet
	DerivationIndex uint32
	// The position of the output in the blockchain.
	ChainPosition ChainPosition
}

func (r *LocalOutput) Destroy() {
	FfiDestroyerOutPoint{}.Destroy(r.Outpoint)
	FfiDestroyerTxOut{}.Destroy(r.Txout)
	FfiDestroyerKeychainKind{}.Destroy(r.Keychain)
	FfiDestroyerBool{}.Destroy(r.IsSpent)
	FfiDestroyerUint32{}.Destroy(r.DerivationIndex)
	FfiDestroyerChainPosition{}.Destroy(r.ChainPosition)
}

type FfiConverterLocalOutput struct{}

var FfiConverterLocalOutputINSTANCE = FfiConverterLocalOutput{}

func (c FfiConverterLocalOutput) Lift(rb RustBufferI) LocalOutput {
	return LiftFromRustBuffer[LocalOutput](c, rb)
}

func (c FfiConverterLocalOutput) Read(reader io.Reader) LocalOutput {
	return LocalOutput{
		FfiConverterOutPointINSTANCE.Read(reader),
		FfiConverterTxOutINSTANCE.Read(reader),
		FfiConverterKeychainKindINSTANCE.Read(reader),
		FfiConverterBoolINSTANCE.Read(reader),
		FfiConverterUint32INSTANCE.Read(reader),
		FfiConverterChainPositionINSTANCE.Read(reader),
	}
}

func (c FfiConverterLocalOutput) Lower(value LocalOutput) C.RustBuffer {
	return LowerIntoRustBuffer[LocalOutput](c, value)
}

func (c FfiConverterLocalOutput) Write(writer io.Writer, value LocalOutput) {
	FfiConverterOutPointINSTANCE.Write(writer, value.Outpoint)
	FfiConverterTxOutINSTANCE.Write(writer, value.Txout)
	FfiConverterKeychainKindINSTANCE.Write(writer, value.Keychain)
	FfiConverterBoolINSTANCE.Write(writer, value.IsSpent)
	FfiConverterUint32INSTANCE.Write(writer, value.DerivationIndex)
	FfiConverterChainPositionINSTANCE.Write(writer, value.ChainPosition)
}

type FfiDestroyerLocalOutput struct{}

func (_ FfiDestroyerLocalOutput) Destroy(value LocalOutput) {
	value.Destroy()
}

type OutPoint struct {
	Txid string
	Vout uint32
}

func (r *OutPoint) Destroy() {
	FfiDestroyerString{}.Destroy(r.Txid)
	FfiDestroyerUint32{}.Destroy(r.Vout)
}

type FfiConverterOutPoint struct{}

var FfiConverterOutPointINSTANCE = FfiConverterOutPoint{}

func (c FfiConverterOutPoint) Lift(rb RustBufferI) OutPoint {
	return LiftFromRustBuffer[OutPoint](c, rb)
}

func (c FfiConverterOutPoint) Read(reader io.Reader) OutPoint {
	return OutPoint{
		FfiConverterStringINSTANCE.Read(reader),
		FfiConverterUint32INSTANCE.Read(reader),
	}
}

func (c FfiConverterOutPoint) Lower(value OutPoint) C.RustBuffer {
	return LowerIntoRustBuffer[OutPoint](c, value)
}

func (c FfiConverterOutPoint) Write(writer io.Writer, value OutPoint) {
	FfiConverterStringINSTANCE.Write(writer, value.Txid)
	FfiConverterUint32INSTANCE.Write(writer, value.Vout)
}

type FfiDestroyerOutPoint struct{}

func (_ FfiDestroyerOutPoint) Destroy(value OutPoint) {
	value.Destroy()
}

type ScriptAmount struct {
	Script *Script
	Amount *Amount
}

func (r *ScriptAmount) Destroy() {
	FfiDestroyerScript{}.Destroy(r.Script)
	FfiDestroyerAmount{}.Destroy(r.Amount)
}

type FfiConverterScriptAmount struct{}

var FfiConverterScriptAmountINSTANCE = FfiConverterScriptAmount{}

func (c FfiConverterScriptAmount) Lift(rb RustBufferI) ScriptAmount {
	return LiftFromRustBuffer[ScriptAmount](c, rb)
}

func (c FfiConverterScriptAmount) Read(reader io.Reader) ScriptAmount {
	return ScriptAmount{
		FfiConverterScriptINSTANCE.Read(reader),
		FfiConverterAmountINSTANCE.Read(reader),
	}
}

func (c FfiConverterScriptAmount) Lower(value ScriptAmount) C.RustBuffer {
	return LowerIntoRustBuffer[ScriptAmount](c, value)
}

func (c FfiConverterScriptAmount) Write(writer io.Writer, value ScriptAmount) {
	FfiConverterScriptINSTANCE.Write(writer, value.Script)
	FfiConverterAmountINSTANCE.Write(writer, value.Amount)
}

type FfiDestroyerScriptAmount struct{}

func (_ FfiDestroyerScriptAmount) Destroy(value ScriptAmount) {
	value.Destroy()
}

type SentAndReceivedValues struct {
	Sent     *Amount
	Received *Amount
}

func (r *SentAndReceivedValues) Destroy() {
	FfiDestroyerAmount{}.Destroy(r.Sent)
	FfiDestroyerAmount{}.Destroy(r.Received)
}

type FfiConverterSentAndReceivedValues struct{}

var FfiConverterSentAndReceivedValuesINSTANCE = FfiConverterSentAndReceivedValues{}

func (c FfiConverterSentAndReceivedValues) Lift(rb RustBufferI) SentAndReceivedValues {
	return LiftFromRustBuffer[SentAndReceivedValues](c, rb)
}

func (c FfiConverterSentAndReceivedValues) Read(reader io.Reader) SentAndReceivedValues {
	return SentAndReceivedValues{
		FfiConverterAmountINSTANCE.Read(reader),
		FfiConverterAmountINSTANCE.Read(reader),
	}
}

func (c FfiConverterSentAndReceivedValues) Lower(value SentAndReceivedValues) C.RustBuffer {
	return LowerIntoRustBuffer[SentAndReceivedValues](c, value)
}

func (c FfiConverterSentAndReceivedValues) Write(writer io.Writer, value SentAndReceivedValues) {
	FfiConverterAmountINSTANCE.Write(writer, value.Sent)
	FfiConverterAmountINSTANCE.Write(writer, value.Received)
}

type FfiDestroyerSentAndReceivedValues struct{}

func (_ FfiDestroyerSentAndReceivedValues) Destroy(value SentAndReceivedValues) {
	value.Destroy()
}

// Response to an ElectrumClient.server_features request.
type ServerFeaturesRes struct {
	// Server version reported.
	ServerVersion string
	// Hash of the genesis block.
	GenesisHash string
	// Minimum supported version of the protocol.
	ProtocolMin string
	// Maximum supported version of the protocol.
	ProtocolMax string
	// Hash function used to create the `ScriptHash`.
	HashFunction *string
	// Pruned height of the server.
	Pruning *int64
}

func (r *ServerFeaturesRes) Destroy() {
	FfiDestroyerString{}.Destroy(r.ServerVersion)
	FfiDestroyerString{}.Destroy(r.GenesisHash)
	FfiDestroyerString{}.Destroy(r.ProtocolMin)
	FfiDestroyerString{}.Destroy(r.ProtocolMax)
	FfiDestroyerOptionalString{}.Destroy(r.HashFunction)
	FfiDestroyerOptionalInt64{}.Destroy(r.Pruning)
}

type FfiConverterServerFeaturesRes struct{}

var FfiConverterServerFeaturesResINSTANCE = FfiConverterServerFeaturesRes{}

func (c FfiConverterServerFeaturesRes) Lift(rb RustBufferI) ServerFeaturesRes {
	return LiftFromRustBuffer[ServerFeaturesRes](c, rb)
}

func (c FfiConverterServerFeaturesRes) Read(reader io.Reader) ServerFeaturesRes {
	return ServerFeaturesRes{
		FfiConverterStringINSTANCE.Read(reader),
		FfiConverterStringINSTANCE.Read(reader),
		FfiConverterStringINSTANCE.Read(reader),
		FfiConverterStringINSTANCE.Read(reader),
		FfiConverterOptionalStringINSTANCE.Read(reader),
		FfiConverterOptionalInt64INSTANCE.Read(reader),
	}
}

func (c FfiConverterServerFeaturesRes) Lower(value ServerFeaturesRes) C.RustBuffer {
	return LowerIntoRustBuffer[ServerFeaturesRes](c, value)
}

func (c FfiConverterServerFeaturesRes) Write(writer io.Writer, value ServerFeaturesRes) {
	FfiConverterStringINSTANCE.Write(writer, value.ServerVersion)
	FfiConverterStringINSTANCE.Write(writer, value.GenesisHash)
	FfiConverterStringINSTANCE.Write(writer, value.ProtocolMin)
	FfiConverterStringINSTANCE.Write(writer, value.ProtocolMax)
	FfiConverterOptionalStringINSTANCE.Write(writer, value.HashFunction)
	FfiConverterOptionalInt64INSTANCE.Write(writer, value.Pruning)
}

type FfiDestroyerServerFeaturesRes struct{}

func (_ FfiDestroyerServerFeaturesRes) Destroy(value ServerFeaturesRes) {
	value.Destroy()
}

// Options for a software signer.
//
// Adjust the behavior of our software signers and the way a transaction is finalized.
type SignOptions struct {
	// Whether the signer should trust the `witness_utxo`, if the `non_witness_utxo` hasn't been
	// provided
	//
	// Defaults to `false` to mitigate the "SegWit bug" which could trick the wallet into
	// paying a fee larger than expected.
	//
	// Some wallets, especially if relatively old, might not provide the `non_witness_utxo` for
	// SegWit transactions in the PSBT they generate: in those cases setting this to `true`
	// should correctly produce a signature, at the expense of an increased trust in the creator
	// of the PSBT.
	//
	// For more details see: <https://blog.trezor.io/details-of-firmware-updates-for-trezor-one-version-1-9-1-and-trezor-model-t-version-2-3-1-1eba8f60f2dd>
	TrustWitnessUtxo bool
	// Whether the wallet should assume a specific height has been reached when trying to finalize
	// a transaction
	//
	// The wallet will only "use" a timelock to satisfy the spending policy of an input if the
	// timelock height has already been reached. This option allows overriding the "current height" to let the
	// wallet use timelocks in the future to spend a coin.
	AssumeHeight *uint32
	// Whether the signer should use the `sighash_type` set in the PSBT when signing, no matter
	// what its value is
	//
	// Defaults to `false` which will only allow signing using `SIGHASH_ALL`.
	AllowAllSighashes bool
	// Whether to try finalizing the PSBT after the inputs are signed.
	//
	// Defaults to `true` which will try finalizing PSBT after inputs are signed.
	TryFinalize bool
	// Whether we should try to sign a taproot transaction with the taproot internal key
	// or not. This option is ignored if we're signing a non-taproot PSBT.
	//
	// Defaults to `true`, i.e., we always try to sign with the taproot internal key.
	SignWithTapInternalKey bool
	// Whether we should grind ECDSA signature to ensure signing with low r
	// or not.
	// Defaults to `true`, i.e., we always grind ECDSA signature to sign with low r.
	AllowGrinding bool
}

func (r *SignOptions) Destroy() {
	FfiDestroyerBool{}.Destroy(r.TrustWitnessUtxo)
	FfiDestroyerOptionalUint32{}.Destroy(r.AssumeHeight)
	FfiDestroyerBool{}.Destroy(r.AllowAllSighashes)
	FfiDestroyerBool{}.Destroy(r.TryFinalize)
	FfiDestroyerBool{}.Destroy(r.SignWithTapInternalKey)
	FfiDestroyerBool{}.Destroy(r.AllowGrinding)
}

type FfiConverterSignOptions struct{}

var FfiConverterSignOptionsINSTANCE = FfiConverterSignOptions{}

func (c FfiConverterSignOptions) Lift(rb RustBufferI) SignOptions {
	return LiftFromRustBuffer[SignOptions](c, rb)
}

func (c FfiConverterSignOptions) Read(reader io.Reader) SignOptions {
	return SignOptions{
		FfiConverterBoolINSTANCE.Read(reader),
		FfiConverterOptionalUint32INSTANCE.Read(reader),
		FfiConverterBoolINSTANCE.Read(reader),
		FfiConverterBoolINSTANCE.Read(reader),
		FfiConverterBoolINSTANCE.Read(reader),
		FfiConverterBoolINSTANCE.Read(reader),
	}
}

func (c FfiConverterSignOptions) Lower(value SignOptions) C.RustBuffer {
	return LowerIntoRustBuffer[SignOptions](c, value)
}

func (c FfiConverterSignOptions) Write(writer io.Writer, value SignOptions) {
	FfiConverterBoolINSTANCE.Write(writer, value.TrustWitnessUtxo)
	FfiConverterOptionalUint32INSTANCE.Write(writer, value.AssumeHeight)
	FfiConverterBoolINSTANCE.Write(writer, value.AllowAllSighashes)
	FfiConverterBoolINSTANCE.Write(writer, value.TryFinalize)
	FfiConverterBoolINSTANCE.Write(writer, value.SignWithTapInternalKey)
	FfiConverterBoolINSTANCE.Write(writer, value.AllowGrinding)
}

type FfiDestroyerSignOptions struct{}

func (_ FfiDestroyerSignOptions) Destroy(value SignOptions) {
	value.Destroy()
}

type Tx struct {
	Txid     string
	Version  int32
	Locktime uint32
	Size     uint64
	Weight   uint64
	Fee      uint64
	Status   TxStatus
}

func (r *Tx) Destroy() {
	FfiDestroyerString{}.Destroy(r.Txid)
	FfiDestroyerInt32{}.Destroy(r.Version)
	FfiDestroyerUint32{}.Destroy(r.Locktime)
	FfiDestroyerUint64{}.Destroy(r.Size)
	FfiDestroyerUint64{}.Destroy(r.Weight)
	FfiDestroyerUint64{}.Destroy(r.Fee)
	FfiDestroyerTxStatus{}.Destroy(r.Status)
}

type FfiConverterTx struct{}

var FfiConverterTxINSTANCE = FfiConverterTx{}

func (c FfiConverterTx) Lift(rb RustBufferI) Tx {
	return LiftFromRustBuffer[Tx](c, rb)
}

func (c FfiConverterTx) Read(reader io.Reader) Tx {
	return Tx{
		FfiConverterStringINSTANCE.Read(reader),
		FfiConverterInt32INSTANCE.Read(reader),
		FfiConverterUint32INSTANCE.Read(reader),
		FfiConverterUint64INSTANCE.Read(reader),
		FfiConverterUint64INSTANCE.Read(reader),
		FfiConverterUint64INSTANCE.Read(reader),
		FfiConverterTxStatusINSTANCE.Read(reader),
	}
}

func (c FfiConverterTx) Lower(value Tx) C.RustBuffer {
	return LowerIntoRustBuffer[Tx](c, value)
}

func (c FfiConverterTx) Write(writer io.Writer, value Tx) {
	FfiConverterStringINSTANCE.Write(writer, value.Txid)
	FfiConverterInt32INSTANCE.Write(writer, value.Version)
	FfiConverterUint32INSTANCE.Write(writer, value.Locktime)
	FfiConverterUint64INSTANCE.Write(writer, value.Size)
	FfiConverterUint64INSTANCE.Write(writer, value.Weight)
	FfiConverterUint64INSTANCE.Write(writer, value.Fee)
	FfiConverterTxStatusINSTANCE.Write(writer, value.Status)
}

type FfiDestroyerTx struct{}

func (_ FfiDestroyerTx) Destroy(value Tx) {
	value.Destroy()
}

type TxIn struct {
	PreviousOutput OutPoint
	ScriptSig      *Script
	Sequence       uint32
	Witness        [][]uint8
}

func (r *TxIn) Destroy() {
	FfiDestroyerOutPoint{}.Destroy(r.PreviousOutput)
	FfiDestroyerScript{}.Destroy(r.ScriptSig)
	FfiDestroyerUint32{}.Destroy(r.Sequence)
	FfiDestroyerSequenceSequenceUint8{}.Destroy(r.Witness)
}

type FfiConverterTxIn struct{}

var FfiConverterTxInINSTANCE = FfiConverterTxIn{}

func (c FfiConverterTxIn) Lift(rb RustBufferI) TxIn {
	return LiftFromRustBuffer[TxIn](c, rb)
}

func (c FfiConverterTxIn) Read(reader io.Reader) TxIn {
	return TxIn{
		FfiConverterOutPointINSTANCE.Read(reader),
		FfiConverterScriptINSTANCE.Read(reader),
		FfiConverterUint32INSTANCE.Read(reader),
		FfiConverterSequenceSequenceUint8INSTANCE.Read(reader),
	}
}

func (c FfiConverterTxIn) Lower(value TxIn) C.RustBuffer {
	return LowerIntoRustBuffer[TxIn](c, value)
}

func (c FfiConverterTxIn) Write(writer io.Writer, value TxIn) {
	FfiConverterOutPointINSTANCE.Write(writer, value.PreviousOutput)
	FfiConverterScriptINSTANCE.Write(writer, value.ScriptSig)
	FfiConverterUint32INSTANCE.Write(writer, value.Sequence)
	FfiConverterSequenceSequenceUint8INSTANCE.Write(writer, value.Witness)
}

type FfiDestroyerTxIn struct{}

func (_ FfiDestroyerTxIn) Destroy(value TxIn) {
	value.Destroy()
}

// Bitcoin transaction output.
//
// Defines new coins to be created as a result of the transaction,
// along with spending conditions ("script", aka "output script"),
// which an input spending it must satisfy.
//
// An output that is not yet spent by an input is called Unspent Transaction Output ("UTXO").
type TxOut struct {
	// The value of the output, in satoshis.
	Value uint64
	// The script which must be satisfied for the output to be spent.
	ScriptPubkey *Script
}

func (r *TxOut) Destroy() {
	FfiDestroyerUint64{}.Destroy(r.Value)
	FfiDestroyerScript{}.Destroy(r.ScriptPubkey)
}

type FfiConverterTxOut struct{}

var FfiConverterTxOutINSTANCE = FfiConverterTxOut{}

func (c FfiConverterTxOut) Lift(rb RustBufferI) TxOut {
	return LiftFromRustBuffer[TxOut](c, rb)
}

func (c FfiConverterTxOut) Read(reader io.Reader) TxOut {
	return TxOut{
		FfiConverterUint64INSTANCE.Read(reader),
		FfiConverterScriptINSTANCE.Read(reader),
	}
}

func (c FfiConverterTxOut) Lower(value TxOut) C.RustBuffer {
	return LowerIntoRustBuffer[TxOut](c, value)
}

func (c FfiConverterTxOut) Write(writer io.Writer, value TxOut) {
	FfiConverterUint64INSTANCE.Write(writer, value.Value)
	FfiConverterScriptINSTANCE.Write(writer, value.ScriptPubkey)
}

type FfiDestroyerTxOut struct{}

func (_ FfiDestroyerTxOut) Destroy(value TxOut) {
	value.Destroy()
}

type TxStatus struct {
	Confirmed   bool
	BlockHeight *uint32
	BlockHash   *string
	BlockTime   *uint64
}

func (r *TxStatus) Destroy() {
	FfiDestroyerBool{}.Destroy(r.Confirmed)
	FfiDestroyerOptionalUint32{}.Destroy(r.BlockHeight)
	FfiDestroyerOptionalString{}.Destroy(r.BlockHash)
	FfiDestroyerOptionalUint64{}.Destroy(r.BlockTime)
}

type FfiConverterTxStatus struct{}

var FfiConverterTxStatusINSTANCE = FfiConverterTxStatus{}

func (c FfiConverterTxStatus) Lift(rb RustBufferI) TxStatus {
	return LiftFromRustBuffer[TxStatus](c, rb)
}

func (c FfiConverterTxStatus) Read(reader io.Reader) TxStatus {
	return TxStatus{
		FfiConverterBoolINSTANCE.Read(reader),
		FfiConverterOptionalUint32INSTANCE.Read(reader),
		FfiConverterOptionalStringINSTANCE.Read(reader),
		FfiConverterOptionalUint64INSTANCE.Read(reader),
	}
}

func (c FfiConverterTxStatus) Lower(value TxStatus) C.RustBuffer {
	return LowerIntoRustBuffer[TxStatus](c, value)
}

func (c FfiConverterTxStatus) Write(writer io.Writer, value TxStatus) {
	FfiConverterBoolINSTANCE.Write(writer, value.Confirmed)
	FfiConverterOptionalUint32INSTANCE.Write(writer, value.BlockHeight)
	FfiConverterOptionalStringINSTANCE.Write(writer, value.BlockHash)
	FfiConverterOptionalUint64INSTANCE.Write(writer, value.BlockTime)
}

type FfiDestroyerTxStatus struct{}

func (_ FfiDestroyerTxStatus) Destroy(value TxStatus) {
	value.Destroy()
}

type WitnessProgram struct {
	Version uint8
	Program []uint8
}

func (r *WitnessProgram) Destroy() {
	FfiDestroyerUint8{}.Destroy(r.Version)
	FfiDestroyerSequenceUint8{}.Destroy(r.Program)
}

type FfiConverterWitnessProgram struct{}

var FfiConverterWitnessProgramINSTANCE = FfiConverterWitnessProgram{}

func (c FfiConverterWitnessProgram) Lift(rb RustBufferI) WitnessProgram {
	return LiftFromRustBuffer[WitnessProgram](c, rb)
}

func (c FfiConverterWitnessProgram) Read(reader io.Reader) WitnessProgram {
	return WitnessProgram{
		FfiConverterUint8INSTANCE.Read(reader),
		FfiConverterSequenceUint8INSTANCE.Read(reader),
	}
}

func (c FfiConverterWitnessProgram) Lower(value WitnessProgram) C.RustBuffer {
	return LowerIntoRustBuffer[WitnessProgram](c, value)
}

func (c FfiConverterWitnessProgram) Write(writer io.Writer, value WitnessProgram) {
	FfiConverterUint8INSTANCE.Write(writer, value.Version)
	FfiConverterSequenceUint8INSTANCE.Write(writer, value.Program)
}

type FfiDestroyerWitnessProgram struct{}

func (_ FfiDestroyerWitnessProgram) Destroy(value WitnessProgram) {
	value.Destroy()
}

type AddressData interface {
	Destroy()
}
type AddressDataP2pkh struct {
	PubkeyHash string
}

func (e AddressDataP2pkh) Destroy() {
	FfiDestroyerString{}.Destroy(e.PubkeyHash)
}

type AddressDataP2sh struct {
	ScriptHash string
}

func (e AddressDataP2sh) Destroy() {
	FfiDestroyerString{}.Destroy(e.ScriptHash)
}

type AddressDataSegwit struct {
	WitnessProgram WitnessProgram
}

func (e AddressDataSegwit) Destroy() {
	FfiDestroyerWitnessProgram{}.Destroy(e.WitnessProgram)
}

type FfiConverterAddressData struct{}

var FfiConverterAddressDataINSTANCE = FfiConverterAddressData{}

func (c FfiConverterAddressData) Lift(rb RustBufferI) AddressData {
	return LiftFromRustBuffer[AddressData](c, rb)
}

func (c FfiConverterAddressData) Lower(value AddressData) C.RustBuffer {
	return LowerIntoRustBuffer[AddressData](c, value)
}
func (FfiConverterAddressData) Read(reader io.Reader) AddressData {
	id := readInt32(reader)
	switch id {
	case 1:
		return AddressDataP2pkh{
			FfiConverterStringINSTANCE.Read(reader),
		}
	case 2:
		return AddressDataP2sh{
			FfiConverterStringINSTANCE.Read(reader),
		}
	case 3:
		return AddressDataSegwit{
			FfiConverterWitnessProgramINSTANCE.Read(reader),
		}
	default:
		panic(fmt.Sprintf("invalid enum value %v in FfiConverterAddressData.Read()", id))
	}
}

func (FfiConverterAddressData) Write(writer io.Writer, value AddressData) {
	switch variant_value := value.(type) {
	case AddressDataP2pkh:
		writeInt32(writer, 1)
		FfiConverterStringINSTANCE.Write(writer, variant_value.PubkeyHash)
	case AddressDataP2sh:
		writeInt32(writer, 2)
		FfiConverterStringINSTANCE.Write(writer, variant_value.ScriptHash)
	case AddressDataSegwit:
		writeInt32(writer, 3)
		FfiConverterWitnessProgramINSTANCE.Write(writer, variant_value.WitnessProgram)
	default:
		_ = variant_value
		panic(fmt.Sprintf("invalid enum value `%v` in FfiConverterAddressData.Write", value))
	}
}

type FfiDestroyerAddressData struct{}

func (_ FfiDestroyerAddressData) Destroy(value AddressData) {
	value.Destroy()
}

type AddressParseError struct {
	err error
}

// Convience method to turn *AddressParseError into error
// Avoiding treating nil pointer as non nil error interface
func (err *AddressParseError) AsError() error {
	if err == nil {
		return nil
	} else {
		return err
	}
}

func (err AddressParseError) Error() string {
	return fmt.Sprintf("AddressParseError: %s", err.err.Error())
}

func (err AddressParseError) Unwrap() error {
	return err.err
}

// Err* are used for checking error type with `errors.Is`
var ErrAddressParseErrorBase58 = fmt.Errorf("AddressParseErrorBase58")
var ErrAddressParseErrorBech32 = fmt.Errorf("AddressParseErrorBech32")
var ErrAddressParseErrorWitnessVersion = fmt.Errorf("AddressParseErrorWitnessVersion")
var ErrAddressParseErrorWitnessProgram = fmt.Errorf("AddressParseErrorWitnessProgram")
var ErrAddressParseErrorUnknownHrp = fmt.Errorf("AddressParseErrorUnknownHrp")
var ErrAddressParseErrorLegacyAddressTooLong = fmt.Errorf("AddressParseErrorLegacyAddressTooLong")
var ErrAddressParseErrorInvalidBase58PayloadLength = fmt.Errorf("AddressParseErrorInvalidBase58PayloadLength")
var ErrAddressParseErrorInvalidLegacyPrefix = fmt.Errorf("AddressParseErrorInvalidLegacyPrefix")
var ErrAddressParseErrorNetworkValidation = fmt.Errorf("AddressParseErrorNetworkValidation")
var ErrAddressParseErrorOtherAddressParseErr = fmt.Errorf("AddressParseErrorOtherAddressParseErr")

// Variant structs
type AddressParseErrorBase58 struct {
}

func NewAddressParseErrorBase58() *AddressParseError {
	return &AddressParseError{err: &AddressParseErrorBase58{}}
}

func (e AddressParseErrorBase58) destroy() {
}

func (err AddressParseErrorBase58) Error() string {
	return fmt.Sprint("Base58")
}

func (self AddressParseErrorBase58) Is(target error) bool {
	return target == ErrAddressParseErrorBase58
}

type AddressParseErrorBech32 struct {
}

func NewAddressParseErrorBech32() *AddressParseError {
	return &AddressParseError{err: &AddressParseErrorBech32{}}
}

func (e AddressParseErrorBech32) destroy() {
}

func (err AddressParseErrorBech32) Error() string {
	return fmt.Sprint("Bech32")
}

func (self AddressParseErrorBech32) Is(target error) bool {
	return target == ErrAddressParseErrorBech32
}

type AddressParseErrorWitnessVersion struct {
	ErrorMessage string
}

func NewAddressParseErrorWitnessVersion(
	errorMessage string,
) *AddressParseError {
	return &AddressParseError{err: &AddressParseErrorWitnessVersion{
		ErrorMessage: errorMessage}}
}

func (e AddressParseErrorWitnessVersion) destroy() {
	FfiDestroyerString{}.Destroy(e.ErrorMessage)
}

func (err AddressParseErrorWitnessVersion) Error() string {
	return fmt.Sprint("WitnessVersion",
		": ",

		"ErrorMessage=",
		err.ErrorMessage,
	)
}

func (self AddressParseErrorWitnessVersion) Is(target error) bool {
	return target == ErrAddressParseErrorWitnessVersion
}

type AddressParseErrorWitnessProgram struct {
	ErrorMessage string
}

func NewAddressParseErrorWitnessProgram(
	errorMessage string,
) *AddressParseError {
	return &AddressParseError{err: &AddressParseErrorWitnessProgram{
		ErrorMessage: errorMessage}}
}

func (e AddressParseErrorWitnessProgram) destroy() {
	FfiDestroyerString{}.Destroy(e.ErrorMessage)
}

func (err AddressParseErrorWitnessProgram) Error() string {
	return fmt.Sprint("WitnessProgram",
		": ",

		"ErrorMessage=",
		err.ErrorMessage,
	)
}

func (self AddressParseErrorWitnessProgram) Is(target error) bool {
	return target == ErrAddressParseErrorWitnessProgram
}

type AddressParseErrorUnknownHrp struct {
}

func NewAddressParseErrorUnknownHrp() *AddressParseError {
	return &AddressParseError{err: &AddressParseErrorUnknownHrp{}}
}

func (e AddressParseErrorUnknownHrp) destroy() {
}

func (err AddressParseErrorUnknownHrp) Error() string {
	return fmt.Sprint("UnknownHrp")
}

func (self AddressParseErrorUnknownHrp) Is(target error) bool {
	return target == ErrAddressParseErrorUnknownHrp
}

type AddressParseErrorLegacyAddressTooLong struct {
}

func NewAddressParseErrorLegacyAddressTooLong() *AddressParseError {
	return &AddressParseError{err: &AddressParseErrorLegacyAddressTooLong{}}
}

func (e AddressParseErrorLegacyAddressTooLong) destroy() {
}

func (err AddressParseErrorLegacyAddressTooLong) Error() string {
	return fmt.Sprint("LegacyAddressTooLong")
}

func (self AddressParseErrorLegacyAddressTooLong) Is(target error) bool {
	return target == ErrAddressParseErrorLegacyAddressTooLong
}

type AddressParseErrorInvalidBase58PayloadLength struct {
}

func NewAddressParseErrorInvalidBase58PayloadLength() *AddressParseError {
	return &AddressParseError{err: &AddressParseErrorInvalidBase58PayloadLength{}}
}

func (e AddressParseErrorInvalidBase58PayloadLength) destroy() {
}

func (err AddressParseErrorInvalidBase58PayloadLength) Error() string {
	return fmt.Sprint("InvalidBase58PayloadLength")
}

func (self AddressParseErrorInvalidBase58PayloadLength) Is(target error) bool {
	return target == ErrAddressParseErrorInvalidBase58PayloadLength
}

type AddressParseErrorInvalidLegacyPrefix struct {
}

func NewAddressParseErrorInvalidLegacyPrefix() *AddressParseError {
	return &AddressParseError{err: &AddressParseErrorInvalidLegacyPrefix{}}
}

func (e AddressParseErrorInvalidLegacyPrefix) destroy() {
}

func (err AddressParseErrorInvalidLegacyPrefix) Error() string {
	return fmt.Sprint("InvalidLegacyPrefix")
}

func (self AddressParseErrorInvalidLegacyPrefix) Is(target error) bool {
	return target == ErrAddressParseErrorInvalidLegacyPrefix
}

type AddressParseErrorNetworkValidation struct {
}

func NewAddressParseErrorNetworkValidation() *AddressParseError {
	return &AddressParseError{err: &AddressParseErrorNetworkValidation{}}
}

func (e AddressParseErrorNetworkValidation) destroy() {
}

func (err AddressParseErrorNetworkValidation) Error() string {
	return fmt.Sprint("NetworkValidation")
}

func (self AddressParseErrorNetworkValidation) Is(target error) bool {
	return target == ErrAddressParseErrorNetworkValidation
}

type AddressParseErrorOtherAddressParseErr struct {
}

func NewAddressParseErrorOtherAddressParseErr() *AddressParseError {
	return &AddressParseError{err: &AddressParseErrorOtherAddressParseErr{}}
}

func (e AddressParseErrorOtherAddressParseErr) destroy() {
}

func (err AddressParseErrorOtherAddressParseErr) Error() string {
	return fmt.Sprint("OtherAddressParseErr")
}

func (self AddressParseErrorOtherAddressParseErr) Is(target error) bool {
	return target == ErrAddressParseErrorOtherAddressParseErr
}

type FfiConverterAddressParseError struct{}

var FfiConverterAddressParseErrorINSTANCE = FfiConverterAddressParseError{}

func (c FfiConverterAddressParseError) Lift(eb RustBufferI) *AddressParseError {
	return LiftFromRustBuffer[*AddressParseError](c, eb)
}

func (c FfiConverterAddressParseError) Lower(value *AddressParseError) C.RustBuffer {
	return LowerIntoRustBuffer[*AddressParseError](c, value)
}

func (c FfiConverterAddressParseError) Read(reader io.Reader) *AddressParseError {
	errorID := readUint32(reader)

	switch errorID {
	case 1:
		return &AddressParseError{&AddressParseErrorBase58{}}
	case 2:
		return &AddressParseError{&AddressParseErrorBech32{}}
	case 3:
		return &AddressParseError{&AddressParseErrorWitnessVersion{
			ErrorMessage: FfiConverterStringINSTANCE.Read(reader),
		}}
	case 4:
		return &AddressParseError{&AddressParseErrorWitnessProgram{
			ErrorMessage: FfiConverterStringINSTANCE.Read(reader),
		}}
	case 5:
		return &AddressParseError{&AddressParseErrorUnknownHrp{}}
	case 6:
		return &AddressParseError{&AddressParseErrorLegacyAddressTooLong{}}
	case 7:
		return &AddressParseError{&AddressParseErrorInvalidBase58PayloadLength{}}
	case 8:
		return &AddressParseError{&AddressParseErrorInvalidLegacyPrefix{}}
	case 9:
		return &AddressParseError{&AddressParseErrorNetworkValidation{}}
	case 10:
		return &AddressParseError{&AddressParseErrorOtherAddressParseErr{}}
	default:
		panic(fmt.Sprintf("Unknown error code %d in FfiConverterAddressParseError.Read()", errorID))
	}
}

func (c FfiConverterAddressParseError) Write(writer io.Writer, value *AddressParseError) {
	switch variantValue := value.err.(type) {
	case *AddressParseErrorBase58:
		writeInt32(writer, 1)
	case *AddressParseErrorBech32:
		writeInt32(writer, 2)
	case *AddressParseErrorWitnessVersion:
		writeInt32(writer, 3)
		FfiConverterStringINSTANCE.Write(writer, variantValue.ErrorMessage)
	case *AddressParseErrorWitnessProgram:
		writeInt32(writer, 4)
		FfiConverterStringINSTANCE.Write(writer, variantValue.ErrorMessage)
	case *AddressParseErrorUnknownHrp:
		writeInt32(writer, 5)
	case *AddressParseErrorLegacyAddressTooLong:
		writeInt32(writer, 6)
	case *AddressParseErrorInvalidBase58PayloadLength:
		writeInt32(writer, 7)
	case *AddressParseErrorInvalidLegacyPrefix:
		writeInt32(writer, 8)
	case *AddressParseErrorNetworkValidation:
		writeInt32(writer, 9)
	case *AddressParseErrorOtherAddressParseErr:
		writeInt32(writer, 10)
	default:
		_ = variantValue
		panic(fmt.Sprintf("invalid error value `%v` in FfiConverterAddressParseError.Write", value))
	}
}

type FfiDestroyerAddressParseError struct{}

func (_ FfiDestroyerAddressParseError) Destroy(value *AddressParseError) {
	switch variantValue := value.err.(type) {
	case AddressParseErrorBase58:
		variantValue.destroy()
	case AddressParseErrorBech32:
		variantValue.destroy()
	case AddressParseErrorWitnessVersion:
		variantValue.destroy()
	case AddressParseErrorWitnessProgram:
		variantValue.destroy()
	case AddressParseErrorUnknownHrp:
		variantValue.destroy()
	case AddressParseErrorLegacyAddressTooLong:
		variantValue.destroy()
	case AddressParseErrorInvalidBase58PayloadLength:
		variantValue.destroy()
	case AddressParseErrorInvalidLegacyPrefix:
		variantValue.destroy()
	case AddressParseErrorNetworkValidation:
		variantValue.destroy()
	case AddressParseErrorOtherAddressParseErr:
		variantValue.destroy()
	default:
		_ = variantValue
		panic(fmt.Sprintf("invalid error value `%v` in FfiDestroyerAddressParseError.Destroy", value))
	}
}

type Bip32Error struct {
	err error
}

// Convience method to turn *Bip32Error into error
// Avoiding treating nil pointer as non nil error interface
func (err *Bip32Error) AsError() error {
	if err == nil {
		return nil
	} else {
		return err
	}
}

func (err Bip32Error) Error() string {
	return fmt.Sprintf("Bip32Error: %s", err.err.Error())
}

func (err Bip32Error) Unwrap() error {
	return err.err
}

// Err* are used for checking error type with `errors.Is`
var ErrBip32ErrorCannotDeriveFromHardenedKey = fmt.Errorf("Bip32ErrorCannotDeriveFromHardenedKey")
var ErrBip32ErrorSecp256k1 = fmt.Errorf("Bip32ErrorSecp256k1")
var ErrBip32ErrorInvalidChildNumber = fmt.Errorf("Bip32ErrorInvalidChildNumber")
var ErrBip32ErrorInvalidChildNumberFormat = fmt.Errorf("Bip32ErrorInvalidChildNumberFormat")
var ErrBip32ErrorInvalidDerivationPathFormat = fmt.Errorf("Bip32ErrorInvalidDerivationPathFormat")
var ErrBip32ErrorUnknownVersion = fmt.Errorf("Bip32ErrorUnknownVersion")
var ErrBip32ErrorWrongExtendedKeyLength = fmt.Errorf("Bip32ErrorWrongExtendedKeyLength")
var ErrBip32ErrorBase58 = fmt.Errorf("Bip32ErrorBase58")
var ErrBip32ErrorHex = fmt.Errorf("Bip32ErrorHex")
var ErrBip32ErrorInvalidPublicKeyHexLength = fmt.Errorf("Bip32ErrorInvalidPublicKeyHexLength")
var ErrBip32ErrorUnknownError = fmt.Errorf("Bip32ErrorUnknownError")

// Variant structs
type Bip32ErrorCannotDeriveFromHardenedKey struct {
}

func NewBip32ErrorCannotDeriveFromHardenedKey() *Bip32Error {
	return &Bip32Error{err: &Bip32ErrorCannotDeriveFromHardenedKey{}}
}

func (e Bip32ErrorCannotDeriveFromHardenedKey) destroy() {
}

func (err Bip32ErrorCannotDeriveFromHardenedKey) Error() string {
	return fmt.Sprint("CannotDeriveFromHardenedKey")
}

func (self Bip32ErrorCannotDeriveFromHardenedKey) Is(target error) bool {
	return target == ErrBip32ErrorCannotDeriveFromHardenedKey
}

type Bip32ErrorSecp256k1 struct {
	ErrorMessage string
}

func NewBip32ErrorSecp256k1(
	errorMessage string,
) *Bip32Error {
	return &Bip32Error{err: &Bip32ErrorSecp256k1{
		ErrorMessage: errorMessage}}
}

func (e Bip32ErrorSecp256k1) destroy() {
	FfiDestroyerString{}.Destroy(e.ErrorMessage)
}

func (err Bip32ErrorSecp256k1) Error() string {
	return fmt.Sprint("Secp256k1",
		": ",

		"ErrorMessage=",
		err.ErrorMessage,
	)
}

func (self Bip32ErrorSecp256k1) Is(target error) bool {
	return target == ErrBip32ErrorSecp256k1
}

type Bip32ErrorInvalidChildNumber struct {
	ChildNumber uint32
}

func NewBip32ErrorInvalidChildNumber(
	childNumber uint32,
) *Bip32Error {
	return &Bip32Error{err: &Bip32ErrorInvalidChildNumber{
		ChildNumber: childNumber}}
}

func (e Bip32ErrorInvalidChildNumber) destroy() {
	FfiDestroyerUint32{}.Destroy(e.ChildNumber)
}

func (err Bip32ErrorInvalidChildNumber) Error() string {
	return fmt.Sprint("InvalidChildNumber",
		": ",

		"ChildNumber=",
		err.ChildNumber,
	)
}

func (self Bip32ErrorInvalidChildNumber) Is(target error) bool {
	return target == ErrBip32ErrorInvalidChildNumber
}

type Bip32ErrorInvalidChildNumberFormat struct {
}

func NewBip32ErrorInvalidChildNumberFormat() *Bip32Error {
	return &Bip32Error{err: &Bip32ErrorInvalidChildNumberFormat{}}
}

func (e Bip32ErrorInvalidChildNumberFormat) destroy() {
}

func (err Bip32ErrorInvalidChildNumberFormat) Error() string {
	return fmt.Sprint("InvalidChildNumberFormat")
}

func (self Bip32ErrorInvalidChildNumberFormat) Is(target error) bool {
	return target == ErrBip32ErrorInvalidChildNumberFormat
}

type Bip32ErrorInvalidDerivationPathFormat struct {
}

func NewBip32ErrorInvalidDerivationPathFormat() *Bip32Error {
	return &Bip32Error{err: &Bip32ErrorInvalidDerivationPathFormat{}}
}

func (e Bip32ErrorInvalidDerivationPathFormat) destroy() {
}

func (err Bip32ErrorInvalidDerivationPathFormat) Error() string {
	return fmt.Sprint("InvalidDerivationPathFormat")
}

func (self Bip32ErrorInvalidDerivationPathFormat) Is(target error) bool {
	return target == ErrBip32ErrorInvalidDerivationPathFormat
}

type Bip32ErrorUnknownVersion struct {
	Version string
}

func NewBip32ErrorUnknownVersion(
	version string,
) *Bip32Error {
	return &Bip32Error{err: &Bip32ErrorUnknownVersion{
		Version: version}}
}

func (e Bip32ErrorUnknownVersion) destroy() {
	FfiDestroyerString{}.Destroy(e.Version)
}

func (err Bip32ErrorUnknownVersion) Error() string {
	return fmt.Sprint("UnknownVersion",
		": ",

		"Version=",
		err.Version,
	)
}

func (self Bip32ErrorUnknownVersion) Is(target error) bool {
	return target == ErrBip32ErrorUnknownVersion
}

type Bip32ErrorWrongExtendedKeyLength struct {
	Length uint32
}

func NewBip32ErrorWrongExtendedKeyLength(
	length uint32,
) *Bip32Error {
	return &Bip32Error{err: &Bip32ErrorWrongExtendedKeyLength{
		Length: length}}
}

func (e Bip32ErrorWrongExtendedKeyLength) destroy() {
	FfiDestroyerUint32{}.Destroy(e.Length)
}

func (err Bip32ErrorWrongExtendedKeyLength) Error() string {
	return fmt.Sprint("WrongExtendedKeyLength",
		": ",

		"Length=",
		err.Length,
	)
}

func (self Bip32ErrorWrongExtendedKeyLength) Is(target error) bool {
	return target == ErrBip32ErrorWrongExtendedKeyLength
}

type Bip32ErrorBase58 struct {
	ErrorMessage string
}

func NewBip32ErrorBase58(
	errorMessage string,
) *Bip32Error {
	return &Bip32Error{err: &Bip32ErrorBase58{
		ErrorMessage: errorMessage}}
}

func (e Bip32ErrorBase58) destroy() {
	FfiDestroyerString{}.Destroy(e.ErrorMessage)
}

func (err Bip32ErrorBase58) Error() string {
	return fmt.Sprint("Base58",
		": ",

		"ErrorMessage=",
		err.ErrorMessage,
	)
}

func (self Bip32ErrorBase58) Is(target error) bool {
	return target == ErrBip32ErrorBase58
}

type Bip32ErrorHex struct {
	ErrorMessage string
}

func NewBip32ErrorHex(
	errorMessage string,
) *Bip32Error {
	return &Bip32Error{err: &Bip32ErrorHex{
		ErrorMessage: errorMessage}}
}

func (e Bip32ErrorHex) destroy() {
	FfiDestroyerString{}.Destroy(e.ErrorMessage)
}

func (err Bip32ErrorHex) Error() string {
	return fmt.Sprint("Hex",
		": ",

		"ErrorMessage=",
		err.ErrorMessage,
	)
}

func (self Bip32ErrorHex) Is(target error) bool {
	return target == ErrBip32ErrorHex
}

type Bip32ErrorInvalidPublicKeyHexLength struct {
	Length uint32
}

func NewBip32ErrorInvalidPublicKeyHexLength(
	length uint32,
) *Bip32Error {
	return &Bip32Error{err: &Bip32ErrorInvalidPublicKeyHexLength{
		Length: length}}
}

func (e Bip32ErrorInvalidPublicKeyHexLength) destroy() {
	FfiDestroyerUint32{}.Destroy(e.Length)
}

func (err Bip32ErrorInvalidPublicKeyHexLength) Error() string {
	return fmt.Sprint("InvalidPublicKeyHexLength",
		": ",

		"Length=",
		err.Length,
	)
}

func (self Bip32ErrorInvalidPublicKeyHexLength) Is(target error) bool {
	return target == ErrBip32ErrorInvalidPublicKeyHexLength
}

type Bip32ErrorUnknownError struct {
	ErrorMessage string
}

func NewBip32ErrorUnknownError(
	errorMessage string,
) *Bip32Error {
	return &Bip32Error{err: &Bip32ErrorUnknownError{
		ErrorMessage: errorMessage}}
}

func (e Bip32ErrorUnknownError) destroy() {
	FfiDestroyerString{}.Destroy(e.ErrorMessage)
}

func (err Bip32ErrorUnknownError) Error() string {
	return fmt.Sprint("UnknownError",
		": ",

		"ErrorMessage=",
		err.ErrorMessage,
	)
}

func (self Bip32ErrorUnknownError) Is(target error) bool {
	return target == ErrBip32ErrorUnknownError
}

type FfiConverterBip32Error struct{}

var FfiConverterBip32ErrorINSTANCE = FfiConverterBip32Error{}

func (c FfiConverterBip32Error) Lift(eb RustBufferI) *Bip32Error {
	return LiftFromRustBuffer[*Bip32Error](c, eb)
}

func (c FfiConverterBip32Error) Lower(value *Bip32Error) C.RustBuffer {
	return LowerIntoRustBuffer[*Bip32Error](c, value)
}

func (c FfiConverterBip32Error) Read(reader io.Reader) *Bip32Error {
	errorID := readUint32(reader)

	switch errorID {
	case 1:
		return &Bip32Error{&Bip32ErrorCannotDeriveFromHardenedKey{}}
	case 2:
		return &Bip32Error{&Bip32ErrorSecp256k1{
			ErrorMessage: FfiConverterStringINSTANCE.Read(reader),
		}}
	case 3:
		return &Bip32Error{&Bip32ErrorInvalidChildNumber{
			ChildNumber: FfiConverterUint32INSTANCE.Read(reader),
		}}
	case 4:
		return &Bip32Error{&Bip32ErrorInvalidChildNumberFormat{}}
	case 5:
		return &Bip32Error{&Bip32ErrorInvalidDerivationPathFormat{}}
	case 6:
		return &Bip32Error{&Bip32ErrorUnknownVersion{
			Version: FfiConverterStringINSTANCE.Read(reader),
		}}
	case 7:
		return &Bip32Error{&Bip32ErrorWrongExtendedKeyLength{
			Length: FfiConverterUint32INSTANCE.Read(reader),
		}}
	case 8:
		return &Bip32Error{&Bip32ErrorBase58{
			ErrorMessage: FfiConverterStringINSTANCE.Read(reader),
		}}
	case 9:
		return &Bip32Error{&Bip32ErrorHex{
			ErrorMessage: FfiConverterStringINSTANCE.Read(reader),
		}}
	case 10:
		return &Bip32Error{&Bip32ErrorInvalidPublicKeyHexLength{
			Length: FfiConverterUint32INSTANCE.Read(reader),
		}}
	case 11:
		return &Bip32Error{&Bip32ErrorUnknownError{
			ErrorMessage: FfiConverterStringINSTANCE.Read(reader),
		}}
	default:
		panic(fmt.Sprintf("Unknown error code %d in FfiConverterBip32Error.Read()", errorID))
	}
}

func (c FfiConverterBip32Error) Write(writer io.Writer, value *Bip32Error) {
	switch variantValue := value.err.(type) {
	case *Bip32ErrorCannotDeriveFromHardenedKey:
		writeInt32(writer, 1)
	case *Bip32ErrorSecp256k1:
		writeInt32(writer, 2)
		FfiConverterStringINSTANCE.Write(writer, variantValue.ErrorMessage)
	case *Bip32ErrorInvalidChildNumber:
		writeInt32(writer, 3)
		FfiConverterUint32INSTANCE.Write(writer, variantValue.ChildNumber)
	case *Bip32ErrorInvalidChildNumberFormat:
		writeInt32(writer, 4)
	case *Bip32ErrorInvalidDerivationPathFormat:
		writeInt32(writer, 5)
	case *Bip32ErrorUnknownVersion:
		writeInt32(writer, 6)
		FfiConverterStringINSTANCE.Write(writer, variantValue.Version)
	case *Bip32ErrorWrongExtendedKeyLength:
		writeInt32(writer, 7)
		FfiConverterUint32INSTANCE.Write(writer, variantValue.Length)
	case *Bip32ErrorBase58:
		writeInt32(writer, 8)
		FfiConverterStringINSTANCE.Write(writer, variantValue.ErrorMessage)
	case *Bip32ErrorHex:
		writeInt32(writer, 9)
		FfiConverterStringINSTANCE.Write(writer, variantValue.ErrorMessage)
	case *Bip32ErrorInvalidPublicKeyHexLength:
		writeInt32(writer, 10)
		FfiConverterUint32INSTANCE.Write(writer, variantValue.Length)
	case *Bip32ErrorUnknownError:
		writeInt32(writer, 11)
		FfiConverterStringINSTANCE.Write(writer, variantValue.ErrorMessage)
	default:
		_ = variantValue
		panic(fmt.Sprintf("invalid error value `%v` in FfiConverterBip32Error.Write", value))
	}
}

type FfiDestroyerBip32Error struct{}

func (_ FfiDestroyerBip32Error) Destroy(value *Bip32Error) {
	switch variantValue := value.err.(type) {
	case Bip32ErrorCannotDeriveFromHardenedKey:
		variantValue.destroy()
	case Bip32ErrorSecp256k1:
		variantValue.destroy()
	case Bip32ErrorInvalidChildNumber:
		variantValue.destroy()
	case Bip32ErrorInvalidChildNumberFormat:
		variantValue.destroy()
	case Bip32ErrorInvalidDerivationPathFormat:
		variantValue.destroy()
	case Bip32ErrorUnknownVersion:
		variantValue.destroy()
	case Bip32ErrorWrongExtendedKeyLength:
		variantValue.destroy()
	case Bip32ErrorBase58:
		variantValue.destroy()
	case Bip32ErrorHex:
		variantValue.destroy()
	case Bip32ErrorInvalidPublicKeyHexLength:
		variantValue.destroy()
	case Bip32ErrorUnknownError:
		variantValue.destroy()
	default:
		_ = variantValue
		panic(fmt.Sprintf("invalid error value `%v` in FfiDestroyerBip32Error.Destroy", value))
	}
}

type Bip39Error struct {
	err error
}

// Convience method to turn *Bip39Error into error
// Avoiding treating nil pointer as non nil error interface
func (err *Bip39Error) AsError() error {
	if err == nil {
		return nil
	} else {
		return err
	}
}

func (err Bip39Error) Error() string {
	return fmt.Sprintf("Bip39Error: %s", err.err.Error())
}

func (err Bip39Error) Unwrap() error {
	return err.err
}

// Err* are used for checking error type with `errors.Is`
var ErrBip39ErrorBadWordCount = fmt.Errorf("Bip39ErrorBadWordCount")
var ErrBip39ErrorUnknownWord = fmt.Errorf("Bip39ErrorUnknownWord")
var ErrBip39ErrorBadEntropyBitCount = fmt.Errorf("Bip39ErrorBadEntropyBitCount")
var ErrBip39ErrorInvalidChecksum = fmt.Errorf("Bip39ErrorInvalidChecksum")
var ErrBip39ErrorAmbiguousLanguages = fmt.Errorf("Bip39ErrorAmbiguousLanguages")

// Variant structs
type Bip39ErrorBadWordCount struct {
	WordCount uint64
}

func NewBip39ErrorBadWordCount(
	wordCount uint64,
) *Bip39Error {
	return &Bip39Error{err: &Bip39ErrorBadWordCount{
		WordCount: wordCount}}
}

func (e Bip39ErrorBadWordCount) destroy() {
	FfiDestroyerUint64{}.Destroy(e.WordCount)
}

func (err Bip39ErrorBadWordCount) Error() string {
	return fmt.Sprint("BadWordCount",
		": ",

		"WordCount=",
		err.WordCount,
	)
}

func (self Bip39ErrorBadWordCount) Is(target error) bool {
	return target == ErrBip39ErrorBadWordCount
}

type Bip39ErrorUnknownWord struct {
	Index uint64
}

func NewBip39ErrorUnknownWord(
	index uint64,
) *Bip39Error {
	return &Bip39Error{err: &Bip39ErrorUnknownWord{
		Index: index}}
}

func (e Bip39ErrorUnknownWord) destroy() {
	FfiDestroyerUint64{}.Destroy(e.Index)
}

func (err Bip39ErrorUnknownWord) Error() string {
	return fmt.Sprint("UnknownWord",
		": ",

		"Index=",
		err.Index,
	)
}

func (self Bip39ErrorUnknownWord) Is(target error) bool {
	return target == ErrBip39ErrorUnknownWord
}

type Bip39ErrorBadEntropyBitCount struct {
	BitCount uint64
}

func NewBip39ErrorBadEntropyBitCount(
	bitCount uint64,
) *Bip39Error {
	return &Bip39Error{err: &Bip39ErrorBadEntropyBitCount{
		BitCount: bitCount}}
}

func (e Bip39ErrorBadEntropyBitCount) destroy() {
	FfiDestroyerUint64{}.Destroy(e.BitCount)
}

func (err Bip39ErrorBadEntropyBitCount) Error() string {
	return fmt.Sprint("BadEntropyBitCount",
		": ",

		"BitCount=",
		err.BitCount,
	)
}

func (self Bip39ErrorBadEntropyBitCount) Is(target error) bool {
	return target == ErrBip39ErrorBadEntropyBitCount
}

type Bip39ErrorInvalidChecksum struct {
}

func NewBip39ErrorInvalidChecksum() *Bip39Error {
	return &Bip39Error{err: &Bip39ErrorInvalidChecksum{}}
}

func (e Bip39ErrorInvalidChecksum) destroy() {
}

func (err Bip39ErrorInvalidChecksum) Error() string {
	return fmt.Sprint("InvalidChecksum")
}

func (self Bip39ErrorInvalidChecksum) Is(target error) bool {
	return target == ErrBip39ErrorInvalidChecksum
}

type Bip39ErrorAmbiguousLanguages struct {
	Languages string
}

func NewBip39ErrorAmbiguousLanguages(
	languages string,
) *Bip39Error {
	return &Bip39Error{err: &Bip39ErrorAmbiguousLanguages{
		Languages: languages}}
}

func (e Bip39ErrorAmbiguousLanguages) destroy() {
	FfiDestroyerString{}.Destroy(e.Languages)
}

func (err Bip39ErrorAmbiguousLanguages) Error() string {
	return fmt.Sprint("AmbiguousLanguages",
		": ",

		"Languages=",
		err.Languages,
	)
}

func (self Bip39ErrorAmbiguousLanguages) Is(target error) bool {
	return target == ErrBip39ErrorAmbiguousLanguages
}

type FfiConverterBip39Error struct{}

var FfiConverterBip39ErrorINSTANCE = FfiConverterBip39Error{}

func (c FfiConverterBip39Error) Lift(eb RustBufferI) *Bip39Error {
	return LiftFromRustBuffer[*Bip39Error](c, eb)
}

func (c FfiConverterBip39Error) Lower(value *Bip39Error) C.RustBuffer {
	return LowerIntoRustBuffer[*Bip39Error](c, value)
}

func (c FfiConverterBip39Error) Read(reader io.Reader) *Bip39Error {
	errorID := readUint32(reader)

	switch errorID {
	case 1:
		return &Bip39Error{&Bip39ErrorBadWordCount{
			WordCount: FfiConverterUint64INSTANCE.Read(reader),
		}}
	case 2:
		return &Bip39Error{&Bip39ErrorUnknownWord{
			Index: FfiConverterUint64INSTANCE.Read(reader),
		}}
	case 3:
		return &Bip39Error{&Bip39ErrorBadEntropyBitCount{
			BitCount: FfiConverterUint64INSTANCE.Read(reader),
		}}
	case 4:
		return &Bip39Error{&Bip39ErrorInvalidChecksum{}}
	case 5:
		return &Bip39Error{&Bip39ErrorAmbiguousLanguages{
			Languages: FfiConverterStringINSTANCE.Read(reader),
		}}
	default:
		panic(fmt.Sprintf("Unknown error code %d in FfiConverterBip39Error.Read()", errorID))
	}
}

func (c FfiConverterBip39Error) Write(writer io.Writer, value *Bip39Error) {
	switch variantValue := value.err.(type) {
	case *Bip39ErrorBadWordCount:
		writeInt32(writer, 1)
		FfiConverterUint64INSTANCE.Write(writer, variantValue.WordCount)
	case *Bip39ErrorUnknownWord:
		writeInt32(writer, 2)
		FfiConverterUint64INSTANCE.Write(writer, variantValue.Index)
	case *Bip39ErrorBadEntropyBitCount:
		writeInt32(writer, 3)
		FfiConverterUint64INSTANCE.Write(writer, variantValue.BitCount)
	case *Bip39ErrorInvalidChecksum:
		writeInt32(writer, 4)
	case *Bip39ErrorAmbiguousLanguages:
		writeInt32(writer, 5)
		FfiConverterStringINSTANCE.Write(writer, variantValue.Languages)
	default:
		_ = variantValue
		panic(fmt.Sprintf("invalid error value `%v` in FfiConverterBip39Error.Write", value))
	}
}

type FfiDestroyerBip39Error struct{}

func (_ FfiDestroyerBip39Error) Destroy(value *Bip39Error) {
	switch variantValue := value.err.(type) {
	case Bip39ErrorBadWordCount:
		variantValue.destroy()
	case Bip39ErrorUnknownWord:
		variantValue.destroy()
	case Bip39ErrorBadEntropyBitCount:
		variantValue.destroy()
	case Bip39ErrorInvalidChecksum:
		variantValue.destroy()
	case Bip39ErrorAmbiguousLanguages:
		variantValue.destroy()
	default:
		_ = variantValue
		panic(fmt.Sprintf("invalid error value `%v` in FfiDestroyerBip39Error.Destroy", value))
	}
}

type CalculateFeeError struct {
	err error
}

// Convience method to turn *CalculateFeeError into error
// Avoiding treating nil pointer as non nil error interface
func (err *CalculateFeeError) AsError() error {
	if err == nil {
		return nil
	} else {
		return err
	}
}

func (err CalculateFeeError) Error() string {
	return fmt.Sprintf("CalculateFeeError: %s", err.err.Error())
}

func (err CalculateFeeError) Unwrap() error {
	return err.err
}

// Err* are used for checking error type with `errors.Is`
var ErrCalculateFeeErrorMissingTxOut = fmt.Errorf("CalculateFeeErrorMissingTxOut")
var ErrCalculateFeeErrorNegativeFee = fmt.Errorf("CalculateFeeErrorNegativeFee")

// Variant structs
type CalculateFeeErrorMissingTxOut struct {
	OutPoints []OutPoint
}

func NewCalculateFeeErrorMissingTxOut(
	outPoints []OutPoint,
) *CalculateFeeError {
	return &CalculateFeeError{err: &CalculateFeeErrorMissingTxOut{
		OutPoints: outPoints}}
}

func (e CalculateFeeErrorMissingTxOut) destroy() {
	FfiDestroyerSequenceOutPoint{}.Destroy(e.OutPoints)
}

func (err CalculateFeeErrorMissingTxOut) Error() string {
	return fmt.Sprint("MissingTxOut",
		": ",

		"OutPoints=",
		err.OutPoints,
	)
}

func (self CalculateFeeErrorMissingTxOut) Is(target error) bool {
	return target == ErrCalculateFeeErrorMissingTxOut
}

type CalculateFeeErrorNegativeFee struct {
	Amount string
}

func NewCalculateFeeErrorNegativeFee(
	amount string,
) *CalculateFeeError {
	return &CalculateFeeError{err: &CalculateFeeErrorNegativeFee{
		Amount: amount}}
}

func (e CalculateFeeErrorNegativeFee) destroy() {
	FfiDestroyerString{}.Destroy(e.Amount)
}

func (err CalculateFeeErrorNegativeFee) Error() string {
	return fmt.Sprint("NegativeFee",
		": ",

		"Amount=",
		err.Amount,
	)
}

func (self CalculateFeeErrorNegativeFee) Is(target error) bool {
	return target == ErrCalculateFeeErrorNegativeFee
}

type FfiConverterCalculateFeeError struct{}

var FfiConverterCalculateFeeErrorINSTANCE = FfiConverterCalculateFeeError{}

func (c FfiConverterCalculateFeeError) Lift(eb RustBufferI) *CalculateFeeError {
	return LiftFromRustBuffer[*CalculateFeeError](c, eb)
}

func (c FfiConverterCalculateFeeError) Lower(value *CalculateFeeError) C.RustBuffer {
	return LowerIntoRustBuffer[*CalculateFeeError](c, value)
}

func (c FfiConverterCalculateFeeError) Read(reader io.Reader) *CalculateFeeError {
	errorID := readUint32(reader)

	switch errorID {
	case 1:
		return &CalculateFeeError{&CalculateFeeErrorMissingTxOut{
			OutPoints: FfiConverterSequenceOutPointINSTANCE.Read(reader),
		}}
	case 2:
		return &CalculateFeeError{&CalculateFeeErrorNegativeFee{
			Amount: FfiConverterStringINSTANCE.Read(reader),
		}}
	default:
		panic(fmt.Sprintf("Unknown error code %d in FfiConverterCalculateFeeError.Read()", errorID))
	}
}

func (c FfiConverterCalculateFeeError) Write(writer io.Writer, value *CalculateFeeError) {
	switch variantValue := value.err.(type) {
	case *CalculateFeeErrorMissingTxOut:
		writeInt32(writer, 1)
		FfiConverterSequenceOutPointINSTANCE.Write(writer, variantValue.OutPoints)
	case *CalculateFeeErrorNegativeFee:
		writeInt32(writer, 2)
		FfiConverterStringINSTANCE.Write(writer, variantValue.Amount)
	default:
		_ = variantValue
		panic(fmt.Sprintf("invalid error value `%v` in FfiConverterCalculateFeeError.Write", value))
	}
}

type FfiDestroyerCalculateFeeError struct{}

func (_ FfiDestroyerCalculateFeeError) Destroy(value *CalculateFeeError) {
	switch variantValue := value.err.(type) {
	case CalculateFeeErrorMissingTxOut:
		variantValue.destroy()
	case CalculateFeeErrorNegativeFee:
		variantValue.destroy()
	default:
		_ = variantValue
		panic(fmt.Sprintf("invalid error value `%v` in FfiDestroyerCalculateFeeError.Destroy", value))
	}
}

type CannotConnectError struct {
	err error
}

// Convience method to turn *CannotConnectError into error
// Avoiding treating nil pointer as non nil error interface
func (err *CannotConnectError) AsError() error {
	if err == nil {
		return nil
	} else {
		return err
	}
}

func (err CannotConnectError) Error() string {
	return fmt.Sprintf("CannotConnectError: %s", err.err.Error())
}

func (err CannotConnectError) Unwrap() error {
	return err.err
}

// Err* are used for checking error type with `errors.Is`
var ErrCannotConnectErrorInclude = fmt.Errorf("CannotConnectErrorInclude")

// Variant structs
type CannotConnectErrorInclude struct {
	Height uint32
}

func NewCannotConnectErrorInclude(
	height uint32,
) *CannotConnectError {
	return &CannotConnectError{err: &CannotConnectErrorInclude{
		Height: height}}
}

func (e CannotConnectErrorInclude) destroy() {
	FfiDestroyerUint32{}.Destroy(e.Height)
}

func (err CannotConnectErrorInclude) Error() string {
	return fmt.Sprint("Include",
		": ",

		"Height=",
		err.Height,
	)
}

func (self CannotConnectErrorInclude) Is(target error) bool {
	return target == ErrCannotConnectErrorInclude
}

type FfiConverterCannotConnectError struct{}

var FfiConverterCannotConnectErrorINSTANCE = FfiConverterCannotConnectError{}

func (c FfiConverterCannotConnectError) Lift(eb RustBufferI) *CannotConnectError {
	return LiftFromRustBuffer[*CannotConnectError](c, eb)
}

func (c FfiConverterCannotConnectError) Lower(value *CannotConnectError) C.RustBuffer {
	return LowerIntoRustBuffer[*CannotConnectError](c, value)
}

func (c FfiConverterCannotConnectError) Read(reader io.Reader) *CannotConnectError {
	errorID := readUint32(reader)

	switch errorID {
	case 1:
		return &CannotConnectError{&CannotConnectErrorInclude{
			Height: FfiConverterUint32INSTANCE.Read(reader),
		}}
	default:
		panic(fmt.Sprintf("Unknown error code %d in FfiConverterCannotConnectError.Read()", errorID))
	}
}

func (c FfiConverterCannotConnectError) Write(writer io.Writer, value *CannotConnectError) {
	switch variantValue := value.err.(type) {
	case *CannotConnectErrorInclude:
		writeInt32(writer, 1)
		FfiConverterUint32INSTANCE.Write(writer, variantValue.Height)
	default:
		_ = variantValue
		panic(fmt.Sprintf("invalid error value `%v` in FfiConverterCannotConnectError.Write", value))
	}
}

type FfiDestroyerCannotConnectError struct{}

func (_ FfiDestroyerCannotConnectError) Destroy(value *CannotConnectError) {
	switch variantValue := value.err.(type) {
	case CannotConnectErrorInclude:
		variantValue.destroy()
	default:
		_ = variantValue
		panic(fmt.Sprintf("invalid error value `%v` in FfiDestroyerCannotConnectError.Destroy", value))
	}
}

// Represents the observed position of some chain data.
type ChainPosition interface {
	Destroy()
}

// The chain data is confirmed as it is anchored in the best chain by `A`.
type ChainPositionConfirmed struct {
	ConfirmationBlockTime ConfirmationBlockTime
	Transitively          *string
}

func (e ChainPositionConfirmed) Destroy() {
	FfiDestroyerConfirmationBlockTime{}.Destroy(e.ConfirmationBlockTime)
	FfiDestroyerOptionalString{}.Destroy(e.Transitively)
}

// The chain data is not confirmed and last seen in the mempool at this timestamp.
type ChainPositionUnconfirmed struct {
	Timestamp *uint64
}

func (e ChainPositionUnconfirmed) Destroy() {
	FfiDestroyerOptionalUint64{}.Destroy(e.Timestamp)
}

type FfiConverterChainPosition struct{}

var FfiConverterChainPositionINSTANCE = FfiConverterChainPosition{}

func (c FfiConverterChainPosition) Lift(rb RustBufferI) ChainPosition {
	return LiftFromRustBuffer[ChainPosition](c, rb)
}

func (c FfiConverterChainPosition) Lower(value ChainPosition) C.RustBuffer {
	return LowerIntoRustBuffer[ChainPosition](c, value)
}
func (FfiConverterChainPosition) Read(reader io.Reader) ChainPosition {
	id := readInt32(reader)
	switch id {
	case 1:
		return ChainPositionConfirmed{
			FfiConverterConfirmationBlockTimeINSTANCE.Read(reader),
			FfiConverterOptionalStringINSTANCE.Read(reader),
		}
	case 2:
		return ChainPositionUnconfirmed{
			FfiConverterOptionalUint64INSTANCE.Read(reader),
		}
	default:
		panic(fmt.Sprintf("invalid enum value %v in FfiConverterChainPosition.Read()", id))
	}
}

func (FfiConverterChainPosition) Write(writer io.Writer, value ChainPosition) {
	switch variant_value := value.(type) {
	case ChainPositionConfirmed:
		writeInt32(writer, 1)
		FfiConverterConfirmationBlockTimeINSTANCE.Write(writer, variant_value.ConfirmationBlockTime)
		FfiConverterOptionalStringINSTANCE.Write(writer, variant_value.Transitively)
	case ChainPositionUnconfirmed:
		writeInt32(writer, 2)
		FfiConverterOptionalUint64INSTANCE.Write(writer, variant_value.Timestamp)
	default:
		_ = variant_value
		panic(fmt.Sprintf("invalid enum value `%v` in FfiConverterChainPosition.Write", value))
	}
}

type FfiDestroyerChainPosition struct{}

func (_ FfiDestroyerChainPosition) Destroy(value ChainPosition) {
	value.Destroy()
}

// Policy regarding the use of change outputs when creating a transaction
type ChangeSpendPolicy uint

const (
	// Use both change and non-change outputs (default)
	ChangeSpendPolicyChangeAllowed ChangeSpendPolicy = 1
	// Only use change outputs (see [`TxBuilder::only_spend_change`])
	ChangeSpendPolicyOnlyChange ChangeSpendPolicy = 2
	// Only use non-change outputs
	ChangeSpendPolicyChangeForbidden ChangeSpendPolicy = 3
)

type FfiConverterChangeSpendPolicy struct{}

var FfiConverterChangeSpendPolicyINSTANCE = FfiConverterChangeSpendPolicy{}

func (c FfiConverterChangeSpendPolicy) Lift(rb RustBufferI) ChangeSpendPolicy {
	return LiftFromRustBuffer[ChangeSpendPolicy](c, rb)
}

func (c FfiConverterChangeSpendPolicy) Lower(value ChangeSpendPolicy) C.RustBuffer {
	return LowerIntoRustBuffer[ChangeSpendPolicy](c, value)
}
func (FfiConverterChangeSpendPolicy) Read(reader io.Reader) ChangeSpendPolicy {
	id := readInt32(reader)
	return ChangeSpendPolicy(id)
}

func (FfiConverterChangeSpendPolicy) Write(writer io.Writer, value ChangeSpendPolicy) {
	writeInt32(writer, int32(value))
}

type FfiDestroyerChangeSpendPolicy struct{}

func (_ FfiDestroyerChangeSpendPolicy) Destroy(value ChangeSpendPolicy) {
}

type CreateTxError struct {
	err error
}

// Convience method to turn *CreateTxError into error
// Avoiding treating nil pointer as non nil error interface
func (err *CreateTxError) AsError() error {
	if err == nil {
		return nil
	} else {
		return err
	}
}

func (err CreateTxError) Error() string {
	return fmt.Sprintf("CreateTxError: %s", err.err.Error())
}

func (err CreateTxError) Unwrap() error {
	return err.err
}

// Err* are used for checking error type with `errors.Is`
var ErrCreateTxErrorDescriptor = fmt.Errorf("CreateTxErrorDescriptor")
var ErrCreateTxErrorPolicy = fmt.Errorf("CreateTxErrorPolicy")
var ErrCreateTxErrorSpendingPolicyRequired = fmt.Errorf("CreateTxErrorSpendingPolicyRequired")
var ErrCreateTxErrorVersion0 = fmt.Errorf("CreateTxErrorVersion0")
var ErrCreateTxErrorVersion1Csv = fmt.Errorf("CreateTxErrorVersion1Csv")
var ErrCreateTxErrorLockTime = fmt.Errorf("CreateTxErrorLockTime")
var ErrCreateTxErrorRbfSequenceCsv = fmt.Errorf("CreateTxErrorRbfSequenceCsv")
var ErrCreateTxErrorFeeTooLow = fmt.Errorf("CreateTxErrorFeeTooLow")
var ErrCreateTxErrorFeeRateTooLow = fmt.Errorf("CreateTxErrorFeeRateTooLow")
var ErrCreateTxErrorNoUtxosSelected = fmt.Errorf("CreateTxErrorNoUtxosSelected")
var ErrCreateTxErrorOutputBelowDustLimit = fmt.Errorf("CreateTxErrorOutputBelowDustLimit")
var ErrCreateTxErrorChangePolicyDescriptor = fmt.Errorf("CreateTxErrorChangePolicyDescriptor")
var ErrCreateTxErrorCoinSelection = fmt.Errorf("CreateTxErrorCoinSelection")
var ErrCreateTxErrorInsufficientFunds = fmt.Errorf("CreateTxErrorInsufficientFunds")
var ErrCreateTxErrorNoRecipients = fmt.Errorf("CreateTxErrorNoRecipients")
var ErrCreateTxErrorPsbt = fmt.Errorf("CreateTxErrorPsbt")
var ErrCreateTxErrorMissingKeyOrigin = fmt.Errorf("CreateTxErrorMissingKeyOrigin")
var ErrCreateTxErrorUnknownUtxo = fmt.Errorf("CreateTxErrorUnknownUtxo")
var ErrCreateTxErrorMissingNonWitnessUtxo = fmt.Errorf("CreateTxErrorMissingNonWitnessUtxo")
var ErrCreateTxErrorMiniscriptPsbt = fmt.Errorf("CreateTxErrorMiniscriptPsbt")
var ErrCreateTxErrorPushBytesError = fmt.Errorf("CreateTxErrorPushBytesError")
var ErrCreateTxErrorLockTimeConversionError = fmt.Errorf("CreateTxErrorLockTimeConversionError")

// Variant structs
type CreateTxErrorDescriptor struct {
	ErrorMessage string
}

func NewCreateTxErrorDescriptor(
	errorMessage string,
) *CreateTxError {
	return &CreateTxError{err: &CreateTxErrorDescriptor{
		ErrorMessage: errorMessage}}
}

func (e CreateTxErrorDescriptor) destroy() {
	FfiDestroyerString{}.Destroy(e.ErrorMessage)
}

func (err CreateTxErrorDescriptor) Error() string {
	return fmt.Sprint("Descriptor",
		": ",

		"ErrorMessage=",
		err.ErrorMessage,
	)
}

func (self CreateTxErrorDescriptor) Is(target error) bool {
	return target == ErrCreateTxErrorDescriptor
}

type CreateTxErrorPolicy struct {
	ErrorMessage string
}

func NewCreateTxErrorPolicy(
	errorMessage string,
) *CreateTxError {
	return &CreateTxError{err: &CreateTxErrorPolicy{
		ErrorMessage: errorMessage}}
}

func (e CreateTxErrorPolicy) destroy() {
	FfiDestroyerString{}.Destroy(e.ErrorMessage)
}

func (err CreateTxErrorPolicy) Error() string {
	return fmt.Sprint("Policy",
		": ",

		"ErrorMessage=",
		err.ErrorMessage,
	)
}

func (self CreateTxErrorPolicy) Is(target error) bool {
	return target == ErrCreateTxErrorPolicy
}

type CreateTxErrorSpendingPolicyRequired struct {
	Kind string
}

func NewCreateTxErrorSpendingPolicyRequired(
	kind string,
) *CreateTxError {
	return &CreateTxError{err: &CreateTxErrorSpendingPolicyRequired{
		Kind: kind}}
}

func (e CreateTxErrorSpendingPolicyRequired) destroy() {
	FfiDestroyerString{}.Destroy(e.Kind)
}

func (err CreateTxErrorSpendingPolicyRequired) Error() string {
	return fmt.Sprint("SpendingPolicyRequired",
		": ",

		"Kind=",
		err.Kind,
	)
}

func (self CreateTxErrorSpendingPolicyRequired) Is(target error) bool {
	return target == ErrCreateTxErrorSpendingPolicyRequired
}

type CreateTxErrorVersion0 struct {
}

func NewCreateTxErrorVersion0() *CreateTxError {
	return &CreateTxError{err: &CreateTxErrorVersion0{}}
}

func (e CreateTxErrorVersion0) destroy() {
}

func (err CreateTxErrorVersion0) Error() string {
	return fmt.Sprint("Version0")
}

func (self CreateTxErrorVersion0) Is(target error) bool {
	return target == ErrCreateTxErrorVersion0
}

type CreateTxErrorVersion1Csv struct {
}

func NewCreateTxErrorVersion1Csv() *CreateTxError {
	return &CreateTxError{err: &CreateTxErrorVersion1Csv{}}
}

func (e CreateTxErrorVersion1Csv) destroy() {
}

func (err CreateTxErrorVersion1Csv) Error() string {
	return fmt.Sprint("Version1Csv")
}

func (self CreateTxErrorVersion1Csv) Is(target error) bool {
	return target == ErrCreateTxErrorVersion1Csv
}

type CreateTxErrorLockTime struct {
	Requested string
	Required  string
}

func NewCreateTxErrorLockTime(
	requested string,
	required string,
) *CreateTxError {
	return &CreateTxError{err: &CreateTxErrorLockTime{
		Requested: requested,
		Required:  required}}
}

func (e CreateTxErrorLockTime) destroy() {
	FfiDestroyerString{}.Destroy(e.Requested)
	FfiDestroyerString{}.Destroy(e.Required)
}

func (err CreateTxErrorLockTime) Error() string {
	return fmt.Sprint("LockTime",
		": ",

		"Requested=",
		err.Requested,
		", ",
		"Required=",
		err.Required,
	)
}

func (self CreateTxErrorLockTime) Is(target error) bool {
	return target == ErrCreateTxErrorLockTime
}

type CreateTxErrorRbfSequenceCsv struct {
	Sequence string
	Csv      string
}

func NewCreateTxErrorRbfSequenceCsv(
	sequence string,
	csv string,
) *CreateTxError {
	return &CreateTxError{err: &CreateTxErrorRbfSequenceCsv{
		Sequence: sequence,
		Csv:      csv}}
}

func (e CreateTxErrorRbfSequenceCsv) destroy() {
	FfiDestroyerString{}.Destroy(e.Sequence)
	FfiDestroyerString{}.Destroy(e.Csv)
}

func (err CreateTxErrorRbfSequenceCsv) Error() string {
	return fmt.Sprint("RbfSequenceCsv",
		": ",

		"Sequence=",
		err.Sequence,
		", ",
		"Csv=",
		err.Csv,
	)
}

func (self CreateTxErrorRbfSequenceCsv) Is(target error) bool {
	return target == ErrCreateTxErrorRbfSequenceCsv
}

type CreateTxErrorFeeTooLow struct {
	Required string
}

func NewCreateTxErrorFeeTooLow(
	required string,
) *CreateTxError {
	return &CreateTxError{err: &CreateTxErrorFeeTooLow{
		Required: required}}
}

func (e CreateTxErrorFeeTooLow) destroy() {
	FfiDestroyerString{}.Destroy(e.Required)
}

func (err CreateTxErrorFeeTooLow) Error() string {
	return fmt.Sprint("FeeTooLow",
		": ",

		"Required=",
		err.Required,
	)
}

func (self CreateTxErrorFeeTooLow) Is(target error) bool {
	return target == ErrCreateTxErrorFeeTooLow
}

type CreateTxErrorFeeRateTooLow struct {
	Required string
}

func NewCreateTxErrorFeeRateTooLow(
	required string,
) *CreateTxError {
	return &CreateTxError{err: &CreateTxErrorFeeRateTooLow{
		Required: required}}
}

func (e CreateTxErrorFeeRateTooLow) destroy() {
	FfiDestroyerString{}.Destroy(e.Required)
}

func (err CreateTxErrorFeeRateTooLow) Error() string {
	return fmt.Sprint("FeeRateTooLow",
		": ",

		"Required=",
		err.Required,
	)
}

func (self CreateTxErrorFeeRateTooLow) Is(target error) bool {
	return target == ErrCreateTxErrorFeeRateTooLow
}

type CreateTxErrorNoUtxosSelected struct {
}

func NewCreateTxErrorNoUtxosSelected() *CreateTxError {
	return &CreateTxError{err: &CreateTxErrorNoUtxosSelected{}}
}

func (e CreateTxErrorNoUtxosSelected) destroy() {
}

func (err CreateTxErrorNoUtxosSelected) Error() string {
	return fmt.Sprint("NoUtxosSelected")
}

func (self CreateTxErrorNoUtxosSelected) Is(target error) bool {
	return target == ErrCreateTxErrorNoUtxosSelected
}

type CreateTxErrorOutputBelowDustLimit struct {
	Index uint64
}

func NewCreateTxErrorOutputBelowDustLimit(
	index uint64,
) *CreateTxError {
	return &CreateTxError{err: &CreateTxErrorOutputBelowDustLimit{
		Index: index}}
}

func (e CreateTxErrorOutputBelowDustLimit) destroy() {
	FfiDestroyerUint64{}.Destroy(e.Index)
}

func (err CreateTxErrorOutputBelowDustLimit) Error() string {
	return fmt.Sprint("OutputBelowDustLimit",
		": ",

		"Index=",
		err.Index,
	)
}

func (self CreateTxErrorOutputBelowDustLimit) Is(target error) bool {
	return target == ErrCreateTxErrorOutputBelowDustLimit
}

type CreateTxErrorChangePolicyDescriptor struct {
}

func NewCreateTxErrorChangePolicyDescriptor() *CreateTxError {
	return &CreateTxError{err: &CreateTxErrorChangePolicyDescriptor{}}
}

func (e CreateTxErrorChangePolicyDescriptor) destroy() {
}

func (err CreateTxErrorChangePolicyDescriptor) Error() string {
	return fmt.Sprint("ChangePolicyDescriptor")
}

func (self CreateTxErrorChangePolicyDescriptor) Is(target error) bool {
	return target == ErrCreateTxErrorChangePolicyDescriptor
}

type CreateTxErrorCoinSelection struct {
	ErrorMessage string
}

func NewCreateTxErrorCoinSelection(
	errorMessage string,
) *CreateTxError {
	return &CreateTxError{err: &CreateTxErrorCoinSelection{
		ErrorMessage: errorMessage}}
}

func (e CreateTxErrorCoinSelection) destroy() {
	FfiDestroyerString{}.Destroy(e.ErrorMessage)
}

func (err CreateTxErrorCoinSelection) Error() string {
	return fmt.Sprint("CoinSelection",
		": ",

		"ErrorMessage=",
		err.ErrorMessage,
	)
}

func (self CreateTxErrorCoinSelection) Is(target error) bool {
	return target == ErrCreateTxErrorCoinSelection
}

type CreateTxErrorInsufficientFunds struct {
	Needed    uint64
	Available uint64
}

func NewCreateTxErrorInsufficientFunds(
	needed uint64,
	available uint64,
) *CreateTxError {
	return &CreateTxError{err: &CreateTxErrorInsufficientFunds{
		Needed:    needed,
		Available: available}}
}

func (e CreateTxErrorInsufficientFunds) destroy() {
	FfiDestroyerUint64{}.Destroy(e.Needed)
	FfiDestroyerUint64{}.Destroy(e.Available)
}

func (err CreateTxErrorInsufficientFunds) Error() string {
	return fmt.Sprint("InsufficientFunds",
		": ",

		"Needed=",
		err.Needed,
		", ",
		"Available=",
		err.Available,
	)
}

func (self CreateTxErrorInsufficientFunds) Is(target error) bool {
	return target == ErrCreateTxErrorInsufficientFunds
}

type CreateTxErrorNoRecipients struct {
}

func NewCreateTxErrorNoRecipients() *CreateTxError {
	return &CreateTxError{err: &CreateTxErrorNoRecipients{}}
}

func (e CreateTxErrorNoRecipients) destroy() {
}

func (err CreateTxErrorNoRecipients) Error() string {
	return fmt.Sprint("NoRecipients")
}

func (self CreateTxErrorNoRecipients) Is(target error) bool {
	return target == ErrCreateTxErrorNoRecipients
}

type CreateTxErrorPsbt struct {
	ErrorMessage string
}

func NewCreateTxErrorPsbt(
	errorMessage string,
) *CreateTxError {
	return &CreateTxError{err: &CreateTxErrorPsbt{
		ErrorMessage: errorMessage}}
}

func (e CreateTxErrorPsbt) destroy() {
	FfiDestroyerString{}.Destroy(e.ErrorMessage)
}

func (err CreateTxErrorPsbt) Error() string {
	return fmt.Sprint("Psbt",
		": ",

		"ErrorMessage=",
		err.ErrorMessage,
	)
}

func (self CreateTxErrorPsbt) Is(target error) bool {
	return target == ErrCreateTxErrorPsbt
}

type CreateTxErrorMissingKeyOrigin struct {
	Key string
}

func NewCreateTxErrorMissingKeyOrigin(
	key string,
) *CreateTxError {
	return &CreateTxError{err: &CreateTxErrorMissingKeyOrigin{
		Key: key}}
}

func (e CreateTxErrorMissingKeyOrigin) destroy() {
	FfiDestroyerString{}.Destroy(e.Key)
}

func (err CreateTxErrorMissingKeyOrigin) Error() string {
	return fmt.Sprint("MissingKeyOrigin",
		": ",

		"Key=",
		err.Key,
	)
}

func (self CreateTxErrorMissingKeyOrigin) Is(target error) bool {
	return target == ErrCreateTxErrorMissingKeyOrigin
}

type CreateTxErrorUnknownUtxo struct {
	Outpoint string
}

func NewCreateTxErrorUnknownUtxo(
	outpoint string,
) *CreateTxError {
	return &CreateTxError{err: &CreateTxErrorUnknownUtxo{
		Outpoint: outpoint}}
}

func (e CreateTxErrorUnknownUtxo) destroy() {
	FfiDestroyerString{}.Destroy(e.Outpoint)
}

func (err CreateTxErrorUnknownUtxo) Error() string {
	return fmt.Sprint("UnknownUtxo",
		": ",

		"Outpoint=",
		err.Outpoint,
	)
}

func (self CreateTxErrorUnknownUtxo) Is(target error) bool {
	return target == ErrCreateTxErrorUnknownUtxo
}

type CreateTxErrorMissingNonWitnessUtxo struct {
	Outpoint string
}

func NewCreateTxErrorMissingNonWitnessUtxo(
	outpoint string,
) *CreateTxError {
	return &CreateTxError{err: &CreateTxErrorMissingNonWitnessUtxo{
		Outpoint: outpoint}}
}

func (e CreateTxErrorMissingNonWitnessUtxo) destroy() {
	FfiDestroyerString{}.Destroy(e.Outpoint)
}

func (err CreateTxErrorMissingNonWitnessUtxo) Error() string {
	return fmt.Sprint("MissingNonWitnessUtxo",
		": ",

		"Outpoint=",
		err.Outpoint,
	)
}

func (self CreateTxErrorMissingNonWitnessUtxo) Is(target error) bool {
	return target == ErrCreateTxErrorMissingNonWitnessUtxo
}

type CreateTxErrorMiniscriptPsbt struct {
	ErrorMessage string
}

func NewCreateTxErrorMiniscriptPsbt(
	errorMessage string,
) *CreateTxError {
	return &CreateTxError{err: &CreateTxErrorMiniscriptPsbt{
		ErrorMessage: errorMessage}}
}

func (e CreateTxErrorMiniscriptPsbt) destroy() {
	FfiDestroyerString{}.Destroy(e.ErrorMessage)
}

func (err CreateTxErrorMiniscriptPsbt) Error() string {
	return fmt.Sprint("MiniscriptPsbt",
		": ",

		"ErrorMessage=",
		err.ErrorMessage,
	)
}

func (self CreateTxErrorMiniscriptPsbt) Is(target error) bool {
	return target == ErrCreateTxErrorMiniscriptPsbt
}

type CreateTxErrorPushBytesError struct {
}

func NewCreateTxErrorPushBytesError() *CreateTxError {
	return &CreateTxError{err: &CreateTxErrorPushBytesError{}}
}

func (e CreateTxErrorPushBytesError) destroy() {
}

func (err CreateTxErrorPushBytesError) Error() string {
	return fmt.Sprint("PushBytesError")
}

func (self CreateTxErrorPushBytesError) Is(target error) bool {
	return target == ErrCreateTxErrorPushBytesError
}

type CreateTxErrorLockTimeConversionError struct {
}

func NewCreateTxErrorLockTimeConversionError() *CreateTxError {
	return &CreateTxError{err: &CreateTxErrorLockTimeConversionError{}}
}

func (e CreateTxErrorLockTimeConversionError) destroy() {
}

func (err CreateTxErrorLockTimeConversionError) Error() string {
	return fmt.Sprint("LockTimeConversionError")
}

func (self CreateTxErrorLockTimeConversionError) Is(target error) bool {
	return target == ErrCreateTxErrorLockTimeConversionError
}

type FfiConverterCreateTxError struct{}

var FfiConverterCreateTxErrorINSTANCE = FfiConverterCreateTxError{}

func (c FfiConverterCreateTxError) Lift(eb RustBufferI) *CreateTxError {
	return LiftFromRustBuffer[*CreateTxError](c, eb)
}

func (c FfiConverterCreateTxError) Lower(value *CreateTxError) C.RustBuffer {
	return LowerIntoRustBuffer[*CreateTxError](c, value)
}

func (c FfiConverterCreateTxError) Read(reader io.Reader) *CreateTxError {
	errorID := readUint32(reader)

	switch errorID {
	case 1:
		return &CreateTxError{&CreateTxErrorDescriptor{
			ErrorMessage: FfiConverterStringINSTANCE.Read(reader),
		}}
	case 2:
		return &CreateTxError{&CreateTxErrorPolicy{
			ErrorMessage: FfiConverterStringINSTANCE.Read(reader),
		}}
	case 3:
		return &CreateTxError{&CreateTxErrorSpendingPolicyRequired{
			Kind: FfiConverterStringINSTANCE.Read(reader),
		}}
	case 4:
		return &CreateTxError{&CreateTxErrorVersion0{}}
	case 5:
		return &CreateTxError{&CreateTxErrorVersion1Csv{}}
	case 6:
		return &CreateTxError{&CreateTxErrorLockTime{
			Requested: FfiConverterStringINSTANCE.Read(reader),
			Required:  FfiConverterStringINSTANCE.Read(reader),
		}}
	case 7:
		return &CreateTxError{&CreateTxErrorRbfSequenceCsv{
			Sequence: FfiConverterStringINSTANCE.Read(reader),
			Csv:      FfiConverterStringINSTANCE.Read(reader),
		}}
	case 8:
		return &CreateTxError{&CreateTxErrorFeeTooLow{
			Required: FfiConverterStringINSTANCE.Read(reader),
		}}
	case 9:
		return &CreateTxError{&CreateTxErrorFeeRateTooLow{
			Required: FfiConverterStringINSTANCE.Read(reader),
		}}
	case 10:
		return &CreateTxError{&CreateTxErrorNoUtxosSelected{}}
	case 11:
		return &CreateTxError{&CreateTxErrorOutputBelowDustLimit{
			Index: FfiConverterUint64INSTANCE.Read(reader),
		}}
	case 12:
		return &CreateTxError{&CreateTxErrorChangePolicyDescriptor{}}
	case 13:
		return &CreateTxError{&CreateTxErrorCoinSelection{
			ErrorMessage: FfiConverterStringINSTANCE.Read(reader),
		}}
	case 14:
		return &CreateTxError{&CreateTxErrorInsufficientFunds{
			Needed:    FfiConverterUint64INSTANCE.Read(reader),
			Available: FfiConverterUint64INSTANCE.Read(reader),
		}}
	case 15:
		return &CreateTxError{&CreateTxErrorNoRecipients{}}
	case 16:
		return &CreateTxError{&CreateTxErrorPsbt{
			ErrorMessage: FfiConverterStringINSTANCE.Read(reader),
		}}
	case 17:
		return &CreateTxError{&CreateTxErrorMissingKeyOrigin{
			Key: FfiConverterStringINSTANCE.Read(reader),
		}}
	case 18:
		return &CreateTxError{&CreateTxErrorUnknownUtxo{
			Outpoint: FfiConverterStringINSTANCE.Read(reader),
		}}
	case 19:
		return &CreateTxError{&CreateTxErrorMissingNonWitnessUtxo{
			Outpoint: FfiConverterStringINSTANCE.Read(reader),
		}}
	case 20:
		return &CreateTxError{&CreateTxErrorMiniscriptPsbt{
			ErrorMessage: FfiConverterStringINSTANCE.Read(reader),
		}}
	case 21:
		return &CreateTxError{&CreateTxErrorPushBytesError{}}
	case 22:
		return &CreateTxError{&CreateTxErrorLockTimeConversionError{}}
	default:
		panic(fmt.Sprintf("Unknown error code %d in FfiConverterCreateTxError.Read()", errorID))
	}
}

func (c FfiConverterCreateTxError) Write(writer io.Writer, value *CreateTxError) {
	switch variantValue := value.err.(type) {
	case *CreateTxErrorDescriptor:
		writeInt32(writer, 1)
		FfiConverterStringINSTANCE.Write(writer, variantValue.ErrorMessage)
	case *CreateTxErrorPolicy:
		writeInt32(writer, 2)
		FfiConverterStringINSTANCE.Write(writer, variantValue.ErrorMessage)
	case *CreateTxErrorSpendingPolicyRequired:
		writeInt32(writer, 3)
		FfiConverterStringINSTANCE.Write(writer, variantValue.Kind)
	case *CreateTxErrorVersion0:
		writeInt32(writer, 4)
	case *CreateTxErrorVersion1Csv:
		writeInt32(writer, 5)
	case *CreateTxErrorLockTime:
		writeInt32(writer, 6)
		FfiConverterStringINSTANCE.Write(writer, variantValue.Requested)
		FfiConverterStringINSTANCE.Write(writer, variantValue.Required)
	case *CreateTxErrorRbfSequenceCsv:
		writeInt32(writer, 7)
		FfiConverterStringINSTANCE.Write(writer, variantValue.Sequence)
		FfiConverterStringINSTANCE.Write(writer, variantValue.Csv)
	case *CreateTxErrorFeeTooLow:
		writeInt32(writer, 8)
		FfiConverterStringINSTANCE.Write(writer, variantValue.Required)
	case *CreateTxErrorFeeRateTooLow:
		writeInt32(writer, 9)
		FfiConverterStringINSTANCE.Write(writer, variantValue.Required)
	case *CreateTxErrorNoUtxosSelected:
		writeInt32(writer, 10)
	case *CreateTxErrorOutputBelowDustLimit:
		writeInt32(writer, 11)
		FfiConverterUint64INSTANCE.Write(writer, variantValue.Index)
	case *CreateTxErrorChangePolicyDescriptor:
		writeInt32(writer, 12)
	case *CreateTxErrorCoinSelection:
		writeInt32(writer, 13)
		FfiConverterStringINSTANCE.Write(writer, variantValue.ErrorMessage)
	case *CreateTxErrorInsufficientFunds:
		writeInt32(writer, 14)
		FfiConverterUint64INSTANCE.Write(writer, variantValue.Needed)
		FfiConverterUint64INSTANCE.Write(writer, variantValue.Available)
	case *CreateTxErrorNoRecipients:
		writeInt32(writer, 15)
	case *CreateTxErrorPsbt:
		writeInt32(writer, 16)
		FfiConverterStringINSTANCE.Write(writer, variantValue.ErrorMessage)
	case *CreateTxErrorMissingKeyOrigin:
		writeInt32(writer, 17)
		FfiConverterStringINSTANCE.Write(writer, variantValue.Key)
	case *CreateTxErrorUnknownUtxo:
		writeInt32(writer, 18)
		FfiConverterStringINSTANCE.Write(writer, variantValue.Outpoint)
	case *CreateTxErrorMissingNonWitnessUtxo:
		writeInt32(writer, 19)
		FfiConverterStringINSTANCE.Write(writer, variantValue.Outpoint)
	case *CreateTxErrorMiniscriptPsbt:
		writeInt32(writer, 20)
		FfiConverterStringINSTANCE.Write(writer, variantValue.ErrorMessage)
	case *CreateTxErrorPushBytesError:
		writeInt32(writer, 21)
	case *CreateTxErrorLockTimeConversionError:
		writeInt32(writer, 22)
	default:
		_ = variantValue
		panic(fmt.Sprintf("invalid error value `%v` in FfiConverterCreateTxError.Write", value))
	}
}

type FfiDestroyerCreateTxError struct{}

func (_ FfiDestroyerCreateTxError) Destroy(value *CreateTxError) {
	switch variantValue := value.err.(type) {
	case CreateTxErrorDescriptor:
		variantValue.destroy()
	case CreateTxErrorPolicy:
		variantValue.destroy()
	case CreateTxErrorSpendingPolicyRequired:
		variantValue.destroy()
	case CreateTxErrorVersion0:
		variantValue.destroy()
	case CreateTxErrorVersion1Csv:
		variantValue.destroy()
	case CreateTxErrorLockTime:
		variantValue.destroy()
	case CreateTxErrorRbfSequenceCsv:
		variantValue.destroy()
	case CreateTxErrorFeeTooLow:
		variantValue.destroy()
	case CreateTxErrorFeeRateTooLow:
		variantValue.destroy()
	case CreateTxErrorNoUtxosSelected:
		variantValue.destroy()
	case CreateTxErrorOutputBelowDustLimit:
		variantValue.destroy()
	case CreateTxErrorChangePolicyDescriptor:
		variantValue.destroy()
	case CreateTxErrorCoinSelection:
		variantValue.destroy()
	case CreateTxErrorInsufficientFunds:
		variantValue.destroy()
	case CreateTxErrorNoRecipients:
		variantValue.destroy()
	case CreateTxErrorPsbt:
		variantValue.destroy()
	case CreateTxErrorMissingKeyOrigin:
		variantValue.destroy()
	case CreateTxErrorUnknownUtxo:
		variantValue.destroy()
	case CreateTxErrorMissingNonWitnessUtxo:
		variantValue.destroy()
	case CreateTxErrorMiniscriptPsbt:
		variantValue.destroy()
	case CreateTxErrorPushBytesError:
		variantValue.destroy()
	case CreateTxErrorLockTimeConversionError:
		variantValue.destroy()
	default:
		_ = variantValue
		panic(fmt.Sprintf("invalid error value `%v` in FfiDestroyerCreateTxError.Destroy", value))
	}
}

type CreateWithPersistError struct {
	err error
}

// Convience method to turn *CreateWithPersistError into error
// Avoiding treating nil pointer as non nil error interface
func (err *CreateWithPersistError) AsError() error {
	if err == nil {
		return nil
	} else {
		return err
	}
}

func (err CreateWithPersistError) Error() string {
	return fmt.Sprintf("CreateWithPersistError: %s", err.err.Error())
}

func (err CreateWithPersistError) Unwrap() error {
	return err.err
}

// Err* are used for checking error type with `errors.Is`
var ErrCreateWithPersistErrorPersist = fmt.Errorf("CreateWithPersistErrorPersist")
var ErrCreateWithPersistErrorDataAlreadyExists = fmt.Errorf("CreateWithPersistErrorDataAlreadyExists")
var ErrCreateWithPersistErrorDescriptor = fmt.Errorf("CreateWithPersistErrorDescriptor")

// Variant structs
type CreateWithPersistErrorPersist struct {
	ErrorMessage string
}

func NewCreateWithPersistErrorPersist(
	errorMessage string,
) *CreateWithPersistError {
	return &CreateWithPersistError{err: &CreateWithPersistErrorPersist{
		ErrorMessage: errorMessage}}
}

func (e CreateWithPersistErrorPersist) destroy() {
	FfiDestroyerString{}.Destroy(e.ErrorMessage)
}

func (err CreateWithPersistErrorPersist) Error() string {
	return fmt.Sprint("Persist",
		": ",

		"ErrorMessage=",
		err.ErrorMessage,
	)
}

func (self CreateWithPersistErrorPersist) Is(target error) bool {
	return target == ErrCreateWithPersistErrorPersist
}

type CreateWithPersistErrorDataAlreadyExists struct {
}

func NewCreateWithPersistErrorDataAlreadyExists() *CreateWithPersistError {
	return &CreateWithPersistError{err: &CreateWithPersistErrorDataAlreadyExists{}}
}

func (e CreateWithPersistErrorDataAlreadyExists) destroy() {
}

func (err CreateWithPersistErrorDataAlreadyExists) Error() string {
	return fmt.Sprint("DataAlreadyExists")
}

func (self CreateWithPersistErrorDataAlreadyExists) Is(target error) bool {
	return target == ErrCreateWithPersistErrorDataAlreadyExists
}

type CreateWithPersistErrorDescriptor struct {
	ErrorMessage string
}

func NewCreateWithPersistErrorDescriptor(
	errorMessage string,
) *CreateWithPersistError {
	return &CreateWithPersistError{err: &CreateWithPersistErrorDescriptor{
		ErrorMessage: errorMessage}}
}

func (e CreateWithPersistErrorDescriptor) destroy() {
	FfiDestroyerString{}.Destroy(e.ErrorMessage)
}

func (err CreateWithPersistErrorDescriptor) Error() string {
	return fmt.Sprint("Descriptor",
		": ",

		"ErrorMessage=",
		err.ErrorMessage,
	)
}

func (self CreateWithPersistErrorDescriptor) Is(target error) bool {
	return target == ErrCreateWithPersistErrorDescriptor
}

type FfiConverterCreateWithPersistError struct{}

var FfiConverterCreateWithPersistErrorINSTANCE = FfiConverterCreateWithPersistError{}

func (c FfiConverterCreateWithPersistError) Lift(eb RustBufferI) *CreateWithPersistError {
	return LiftFromRustBuffer[*CreateWithPersistError](c, eb)
}

func (c FfiConverterCreateWithPersistError) Lower(value *CreateWithPersistError) C.RustBuffer {
	return LowerIntoRustBuffer[*CreateWithPersistError](c, value)
}

func (c FfiConverterCreateWithPersistError) Read(reader io.Reader) *CreateWithPersistError {
	errorID := readUint32(reader)

	switch errorID {
	case 1:
		return &CreateWithPersistError{&CreateWithPersistErrorPersist{
			ErrorMessage: FfiConverterStringINSTANCE.Read(reader),
		}}
	case 2:
		return &CreateWithPersistError{&CreateWithPersistErrorDataAlreadyExists{}}
	case 3:
		return &CreateWithPersistError{&CreateWithPersistErrorDescriptor{
			ErrorMessage: FfiConverterStringINSTANCE.Read(reader),
		}}
	default:
		panic(fmt.Sprintf("Unknown error code %d in FfiConverterCreateWithPersistError.Read()", errorID))
	}
}

func (c FfiConverterCreateWithPersistError) Write(writer io.Writer, value *CreateWithPersistError) {
	switch variantValue := value.err.(type) {
	case *CreateWithPersistErrorPersist:
		writeInt32(writer, 1)
		FfiConverterStringINSTANCE.Write(writer, variantValue.ErrorMessage)
	case *CreateWithPersistErrorDataAlreadyExists:
		writeInt32(writer, 2)
	case *CreateWithPersistErrorDescriptor:
		writeInt32(writer, 3)
		FfiConverterStringINSTANCE.Write(writer, variantValue.ErrorMessage)
	default:
		_ = variantValue
		panic(fmt.Sprintf("invalid error value `%v` in FfiConverterCreateWithPersistError.Write", value))
	}
}

type FfiDestroyerCreateWithPersistError struct{}

func (_ FfiDestroyerCreateWithPersistError) Destroy(value *CreateWithPersistError) {
	switch variantValue := value.err.(type) {
	case CreateWithPersistErrorPersist:
		variantValue.destroy()
	case CreateWithPersistErrorDataAlreadyExists:
		variantValue.destroy()
	case CreateWithPersistErrorDescriptor:
		variantValue.destroy()
	default:
		_ = variantValue
		panic(fmt.Sprintf("invalid error value `%v` in FfiDestroyerCreateWithPersistError.Destroy", value))
	}
}

type DescriptorError struct {
	err error
}

// Convience method to turn *DescriptorError into error
// Avoiding treating nil pointer as non nil error interface
func (err *DescriptorError) AsError() error {
	if err == nil {
		return nil
	} else {
		return err
	}
}

func (err DescriptorError) Error() string {
	return fmt.Sprintf("DescriptorError: %s", err.err.Error())
}

func (err DescriptorError) Unwrap() error {
	return err.err
}

// Err* are used for checking error type with `errors.Is`
var ErrDescriptorErrorInvalidHdKeyPath = fmt.Errorf("DescriptorErrorInvalidHdKeyPath")
var ErrDescriptorErrorInvalidDescriptorChecksum = fmt.Errorf("DescriptorErrorInvalidDescriptorChecksum")
var ErrDescriptorErrorHardenedDerivationXpub = fmt.Errorf("DescriptorErrorHardenedDerivationXpub")
var ErrDescriptorErrorMultiPath = fmt.Errorf("DescriptorErrorMultiPath")
var ErrDescriptorErrorKey = fmt.Errorf("DescriptorErrorKey")
var ErrDescriptorErrorPolicy = fmt.Errorf("DescriptorErrorPolicy")
var ErrDescriptorErrorInvalidDescriptorCharacter = fmt.Errorf("DescriptorErrorInvalidDescriptorCharacter")
var ErrDescriptorErrorBip32 = fmt.Errorf("DescriptorErrorBip32")
var ErrDescriptorErrorBase58 = fmt.Errorf("DescriptorErrorBase58")
var ErrDescriptorErrorPk = fmt.Errorf("DescriptorErrorPk")
var ErrDescriptorErrorMiniscript = fmt.Errorf("DescriptorErrorMiniscript")
var ErrDescriptorErrorHex = fmt.Errorf("DescriptorErrorHex")
var ErrDescriptorErrorExternalAndInternalAreTheSame = fmt.Errorf("DescriptorErrorExternalAndInternalAreTheSame")

// Variant structs
type DescriptorErrorInvalidHdKeyPath struct {
}

func NewDescriptorErrorInvalidHdKeyPath() *DescriptorError {
	return &DescriptorError{err: &DescriptorErrorInvalidHdKeyPath{}}
}

func (e DescriptorErrorInvalidHdKeyPath) destroy() {
}

func (err DescriptorErrorInvalidHdKeyPath) Error() string {
	return fmt.Sprint("InvalidHdKeyPath")
}

func (self DescriptorErrorInvalidHdKeyPath) Is(target error) bool {
	return target == ErrDescriptorErrorInvalidHdKeyPath
}

type DescriptorErrorInvalidDescriptorChecksum struct {
}

func NewDescriptorErrorInvalidDescriptorChecksum() *DescriptorError {
	return &DescriptorError{err: &DescriptorErrorInvalidDescriptorChecksum{}}
}

func (e DescriptorErrorInvalidDescriptorChecksum) destroy() {
}

func (err DescriptorErrorInvalidDescriptorChecksum) Error() string {
	return fmt.Sprint("InvalidDescriptorChecksum")
}

func (self DescriptorErrorInvalidDescriptorChecksum) Is(target error) bool {
	return target == ErrDescriptorErrorInvalidDescriptorChecksum
}

type DescriptorErrorHardenedDerivationXpub struct {
}

func NewDescriptorErrorHardenedDerivationXpub() *DescriptorError {
	return &DescriptorError{err: &DescriptorErrorHardenedDerivationXpub{}}
}

func (e DescriptorErrorHardenedDerivationXpub) destroy() {
}

func (err DescriptorErrorHardenedDerivationXpub) Error() string {
	return fmt.Sprint("HardenedDerivationXpub")
}

func (self DescriptorErrorHardenedDerivationXpub) Is(target error) bool {
	return target == ErrDescriptorErrorHardenedDerivationXpub
}

type DescriptorErrorMultiPath struct {
}

func NewDescriptorErrorMultiPath() *DescriptorError {
	return &DescriptorError{err: &DescriptorErrorMultiPath{}}
}

func (e DescriptorErrorMultiPath) destroy() {
}

func (err DescriptorErrorMultiPath) Error() string {
	return fmt.Sprint("MultiPath")
}

func (self DescriptorErrorMultiPath) Is(target error) bool {
	return target == ErrDescriptorErrorMultiPath
}

type DescriptorErrorKey struct {
	ErrorMessage string
}

func NewDescriptorErrorKey(
	errorMessage string,
) *DescriptorError {
	return &DescriptorError{err: &DescriptorErrorKey{
		ErrorMessage: errorMessage}}
}

func (e DescriptorErrorKey) destroy() {
	FfiDestroyerString{}.Destroy(e.ErrorMessage)
}

func (err DescriptorErrorKey) Error() string {
	return fmt.Sprint("Key",
		": ",

		"ErrorMessage=",
		err.ErrorMessage,
	)
}

func (self DescriptorErrorKey) Is(target error) bool {
	return target == ErrDescriptorErrorKey
}

type DescriptorErrorPolicy struct {
	ErrorMessage string
}

func NewDescriptorErrorPolicy(
	errorMessage string,
) *DescriptorError {
	return &DescriptorError{err: &DescriptorErrorPolicy{
		ErrorMessage: errorMessage}}
}

func (e DescriptorErrorPolicy) destroy() {
	FfiDestroyerString{}.Destroy(e.ErrorMessage)
}

func (err DescriptorErrorPolicy) Error() string {
	return fmt.Sprint("Policy",
		": ",

		"ErrorMessage=",
		err.ErrorMessage,
	)
}

func (self DescriptorErrorPolicy) Is(target error) bool {
	return target == ErrDescriptorErrorPolicy
}

type DescriptorErrorInvalidDescriptorCharacter struct {
	Char string
}

func NewDescriptorErrorInvalidDescriptorCharacter(
	char string,
) *DescriptorError {
	return &DescriptorError{err: &DescriptorErrorInvalidDescriptorCharacter{
		Char: char}}
}

func (e DescriptorErrorInvalidDescriptorCharacter) destroy() {
	FfiDestroyerString{}.Destroy(e.Char)
}

func (err DescriptorErrorInvalidDescriptorCharacter) Error() string {
	return fmt.Sprint("InvalidDescriptorCharacter",
		": ",

		"Char=",
		err.Char,
	)
}

func (self DescriptorErrorInvalidDescriptorCharacter) Is(target error) bool {
	return target == ErrDescriptorErrorInvalidDescriptorCharacter
}

type DescriptorErrorBip32 struct {
	ErrorMessage string
}

func NewDescriptorErrorBip32(
	errorMessage string,
) *DescriptorError {
	return &DescriptorError{err: &DescriptorErrorBip32{
		ErrorMessage: errorMessage}}
}

func (e DescriptorErrorBip32) destroy() {
	FfiDestroyerString{}.Destroy(e.ErrorMessage)
}

func (err DescriptorErrorBip32) Error() string {
	return fmt.Sprint("Bip32",
		": ",

		"ErrorMessage=",
		err.ErrorMessage,
	)
}

func (self DescriptorErrorBip32) Is(target error) bool {
	return target == ErrDescriptorErrorBip32
}

type DescriptorErrorBase58 struct {
	ErrorMessage string
}

func NewDescriptorErrorBase58(
	errorMessage string,
) *DescriptorError {
	return &DescriptorError{err: &DescriptorErrorBase58{
		ErrorMessage: errorMessage}}
}

func (e DescriptorErrorBase58) destroy() {
	FfiDestroyerString{}.Destroy(e.ErrorMessage)
}

func (err DescriptorErrorBase58) Error() string {
	return fmt.Sprint("Base58",
		": ",

		"ErrorMessage=",
		err.ErrorMessage,
	)
}

func (self DescriptorErrorBase58) Is(target error) bool {
	return target == ErrDescriptorErrorBase58
}

type DescriptorErrorPk struct {
	ErrorMessage string
}

func NewDescriptorErrorPk(
	errorMessage string,
) *DescriptorError {
	return &DescriptorError{err: &DescriptorErrorPk{
		ErrorMessage: errorMessage}}
}

func (e DescriptorErrorPk) destroy() {
	FfiDestroyerString{}.Destroy(e.ErrorMessage)
}

func (err DescriptorErrorPk) Error() string {
	return fmt.Sprint("Pk",
		": ",

		"ErrorMessage=",
		err.ErrorMessage,
	)
}

func (self DescriptorErrorPk) Is(target error) bool {
	return target == ErrDescriptorErrorPk
}

type DescriptorErrorMiniscript struct {
	ErrorMessage string
}

func NewDescriptorErrorMiniscript(
	errorMessage string,
) *DescriptorError {
	return &DescriptorError{err: &DescriptorErrorMiniscript{
		ErrorMessage: errorMessage}}
}

func (e DescriptorErrorMiniscript) destroy() {
	FfiDestroyerString{}.Destroy(e.ErrorMessage)
}

func (err DescriptorErrorMiniscript) Error() string {
	return fmt.Sprint("Miniscript",
		": ",

		"ErrorMessage=",
		err.ErrorMessage,
	)
}

func (self DescriptorErrorMiniscript) Is(target error) bool {
	return target == ErrDescriptorErrorMiniscript
}

type DescriptorErrorHex struct {
	ErrorMessage string
}

func NewDescriptorErrorHex(
	errorMessage string,
) *DescriptorError {
	return &DescriptorError{err: &DescriptorErrorHex{
		ErrorMessage: errorMessage}}
}

func (e DescriptorErrorHex) destroy() {
	FfiDestroyerString{}.Destroy(e.ErrorMessage)
}

func (err DescriptorErrorHex) Error() string {
	return fmt.Sprint("Hex",
		": ",

		"ErrorMessage=",
		err.ErrorMessage,
	)
}

func (self DescriptorErrorHex) Is(target error) bool {
	return target == ErrDescriptorErrorHex
}

type DescriptorErrorExternalAndInternalAreTheSame struct {
}

func NewDescriptorErrorExternalAndInternalAreTheSame() *DescriptorError {
	return &DescriptorError{err: &DescriptorErrorExternalAndInternalAreTheSame{}}
}

func (e DescriptorErrorExternalAndInternalAreTheSame) destroy() {
}

func (err DescriptorErrorExternalAndInternalAreTheSame) Error() string {
	return fmt.Sprint("ExternalAndInternalAreTheSame")
}

func (self DescriptorErrorExternalAndInternalAreTheSame) Is(target error) bool {
	return target == ErrDescriptorErrorExternalAndInternalAreTheSame
}

type FfiConverterDescriptorError struct{}

var FfiConverterDescriptorErrorINSTANCE = FfiConverterDescriptorError{}

func (c FfiConverterDescriptorError) Lift(eb RustBufferI) *DescriptorError {
	return LiftFromRustBuffer[*DescriptorError](c, eb)
}

func (c FfiConverterDescriptorError) Lower(value *DescriptorError) C.RustBuffer {
	return LowerIntoRustBuffer[*DescriptorError](c, value)
}

func (c FfiConverterDescriptorError) Read(reader io.Reader) *DescriptorError {
	errorID := readUint32(reader)

	switch errorID {
	case 1:
		return &DescriptorError{&DescriptorErrorInvalidHdKeyPath{}}
	case 2:
		return &DescriptorError{&DescriptorErrorInvalidDescriptorChecksum{}}
	case 3:
		return &DescriptorError{&DescriptorErrorHardenedDerivationXpub{}}
	case 4:
		return &DescriptorError{&DescriptorErrorMultiPath{}}
	case 5:
		return &DescriptorError{&DescriptorErrorKey{
			ErrorMessage: FfiConverterStringINSTANCE.Read(reader),
		}}
	case 6:
		return &DescriptorError{&DescriptorErrorPolicy{
			ErrorMessage: FfiConverterStringINSTANCE.Read(reader),
		}}
	case 7:
		return &DescriptorError{&DescriptorErrorInvalidDescriptorCharacter{
			Char: FfiConverterStringINSTANCE.Read(reader),
		}}
	case 8:
		return &DescriptorError{&DescriptorErrorBip32{
			ErrorMessage: FfiConverterStringINSTANCE.Read(reader),
		}}
	case 9:
		return &DescriptorError{&DescriptorErrorBase58{
			ErrorMessage: FfiConverterStringINSTANCE.Read(reader),
		}}
	case 10:
		return &DescriptorError{&DescriptorErrorPk{
			ErrorMessage: FfiConverterStringINSTANCE.Read(reader),
		}}
	case 11:
		return &DescriptorError{&DescriptorErrorMiniscript{
			ErrorMessage: FfiConverterStringINSTANCE.Read(reader),
		}}
	case 12:
		return &DescriptorError{&DescriptorErrorHex{
			ErrorMessage: FfiConverterStringINSTANCE.Read(reader),
		}}
	case 13:
		return &DescriptorError{&DescriptorErrorExternalAndInternalAreTheSame{}}
	default:
		panic(fmt.Sprintf("Unknown error code %d in FfiConverterDescriptorError.Read()", errorID))
	}
}

func (c FfiConverterDescriptorError) Write(writer io.Writer, value *DescriptorError) {
	switch variantValue := value.err.(type) {
	case *DescriptorErrorInvalidHdKeyPath:
		writeInt32(writer, 1)
	case *DescriptorErrorInvalidDescriptorChecksum:
		writeInt32(writer, 2)
	case *DescriptorErrorHardenedDerivationXpub:
		writeInt32(writer, 3)
	case *DescriptorErrorMultiPath:
		writeInt32(writer, 4)
	case *DescriptorErrorKey:
		writeInt32(writer, 5)
		FfiConverterStringINSTANCE.Write(writer, variantValue.ErrorMessage)
	case *DescriptorErrorPolicy:
		writeInt32(writer, 6)
		FfiConverterStringINSTANCE.Write(writer, variantValue.ErrorMessage)
	case *DescriptorErrorInvalidDescriptorCharacter:
		writeInt32(writer, 7)
		FfiConverterStringINSTANCE.Write(writer, variantValue.Char)
	case *DescriptorErrorBip32:
		writeInt32(writer, 8)
		FfiConverterStringINSTANCE.Write(writer, variantValue.ErrorMessage)
	case *DescriptorErrorBase58:
		writeInt32(writer, 9)
		FfiConverterStringINSTANCE.Write(writer, variantValue.ErrorMessage)
	case *DescriptorErrorPk:
		writeInt32(writer, 10)
		FfiConverterStringINSTANCE.Write(writer, variantValue.ErrorMessage)
	case *DescriptorErrorMiniscript:
		writeInt32(writer, 11)
		FfiConverterStringINSTANCE.Write(writer, variantValue.ErrorMessage)
	case *DescriptorErrorHex:
		writeInt32(writer, 12)
		FfiConverterStringINSTANCE.Write(writer, variantValue.ErrorMessage)
	case *DescriptorErrorExternalAndInternalAreTheSame:
		writeInt32(writer, 13)
	default:
		_ = variantValue
		panic(fmt.Sprintf("invalid error value `%v` in FfiConverterDescriptorError.Write", value))
	}
}

type FfiDestroyerDescriptorError struct{}

func (_ FfiDestroyerDescriptorError) Destroy(value *DescriptorError) {
	switch variantValue := value.err.(type) {
	case DescriptorErrorInvalidHdKeyPath:
		variantValue.destroy()
	case DescriptorErrorInvalidDescriptorChecksum:
		variantValue.destroy()
	case DescriptorErrorHardenedDerivationXpub:
		variantValue.destroy()
	case DescriptorErrorMultiPath:
		variantValue.destroy()
	case DescriptorErrorKey:
		variantValue.destroy()
	case DescriptorErrorPolicy:
		variantValue.destroy()
	case DescriptorErrorInvalidDescriptorCharacter:
		variantValue.destroy()
	case DescriptorErrorBip32:
		variantValue.destroy()
	case DescriptorErrorBase58:
		variantValue.destroy()
	case DescriptorErrorPk:
		variantValue.destroy()
	case DescriptorErrorMiniscript:
		variantValue.destroy()
	case DescriptorErrorHex:
		variantValue.destroy()
	case DescriptorErrorExternalAndInternalAreTheSame:
		variantValue.destroy()
	default:
		_ = variantValue
		panic(fmt.Sprintf("invalid error value `%v` in FfiDestroyerDescriptorError.Destroy", value))
	}
}

type DescriptorKeyError struct {
	err error
}

// Convience method to turn *DescriptorKeyError into error
// Avoiding treating nil pointer as non nil error interface
func (err *DescriptorKeyError) AsError() error {
	if err == nil {
		return nil
	} else {
		return err
	}
}

func (err DescriptorKeyError) Error() string {
	return fmt.Sprintf("DescriptorKeyError: %s", err.err.Error())
}

func (err DescriptorKeyError) Unwrap() error {
	return err.err
}

// Err* are used for checking error type with `errors.Is`
var ErrDescriptorKeyErrorParse = fmt.Errorf("DescriptorKeyErrorParse")
var ErrDescriptorKeyErrorInvalidKeyType = fmt.Errorf("DescriptorKeyErrorInvalidKeyType")
var ErrDescriptorKeyErrorBip32 = fmt.Errorf("DescriptorKeyErrorBip32")

// Variant structs
type DescriptorKeyErrorParse struct {
	ErrorMessage string
}

func NewDescriptorKeyErrorParse(
	errorMessage string,
) *DescriptorKeyError {
	return &DescriptorKeyError{err: &DescriptorKeyErrorParse{
		ErrorMessage: errorMessage}}
}

func (e DescriptorKeyErrorParse) destroy() {
	FfiDestroyerString{}.Destroy(e.ErrorMessage)
}

func (err DescriptorKeyErrorParse) Error() string {
	return fmt.Sprint("Parse",
		": ",

		"ErrorMessage=",
		err.ErrorMessage,
	)
}

func (self DescriptorKeyErrorParse) Is(target error) bool {
	return target == ErrDescriptorKeyErrorParse
}

type DescriptorKeyErrorInvalidKeyType struct {
}

func NewDescriptorKeyErrorInvalidKeyType() *DescriptorKeyError {
	return &DescriptorKeyError{err: &DescriptorKeyErrorInvalidKeyType{}}
}

func (e DescriptorKeyErrorInvalidKeyType) destroy() {
}

func (err DescriptorKeyErrorInvalidKeyType) Error() string {
	return fmt.Sprint("InvalidKeyType")
}

func (self DescriptorKeyErrorInvalidKeyType) Is(target error) bool {
	return target == ErrDescriptorKeyErrorInvalidKeyType
}

type DescriptorKeyErrorBip32 struct {
	ErrorMessage string
}

func NewDescriptorKeyErrorBip32(
	errorMessage string,
) *DescriptorKeyError {
	return &DescriptorKeyError{err: &DescriptorKeyErrorBip32{
		ErrorMessage: errorMessage}}
}

func (e DescriptorKeyErrorBip32) destroy() {
	FfiDestroyerString{}.Destroy(e.ErrorMessage)
}

func (err DescriptorKeyErrorBip32) Error() string {
	return fmt.Sprint("Bip32",
		": ",

		"ErrorMessage=",
		err.ErrorMessage,
	)
}

func (self DescriptorKeyErrorBip32) Is(target error) bool {
	return target == ErrDescriptorKeyErrorBip32
}

type FfiConverterDescriptorKeyError struct{}

var FfiConverterDescriptorKeyErrorINSTANCE = FfiConverterDescriptorKeyError{}

func (c FfiConverterDescriptorKeyError) Lift(eb RustBufferI) *DescriptorKeyError {
	return LiftFromRustBuffer[*DescriptorKeyError](c, eb)
}

func (c FfiConverterDescriptorKeyError) Lower(value *DescriptorKeyError) C.RustBuffer {
	return LowerIntoRustBuffer[*DescriptorKeyError](c, value)
}

func (c FfiConverterDescriptorKeyError) Read(reader io.Reader) *DescriptorKeyError {
	errorID := readUint32(reader)

	switch errorID {
	case 1:
		return &DescriptorKeyError{&DescriptorKeyErrorParse{
			ErrorMessage: FfiConverterStringINSTANCE.Read(reader),
		}}
	case 2:
		return &DescriptorKeyError{&DescriptorKeyErrorInvalidKeyType{}}
	case 3:
		return &DescriptorKeyError{&DescriptorKeyErrorBip32{
			ErrorMessage: FfiConverterStringINSTANCE.Read(reader),
		}}
	default:
		panic(fmt.Sprintf("Unknown error code %d in FfiConverterDescriptorKeyError.Read()", errorID))
	}
}

func (c FfiConverterDescriptorKeyError) Write(writer io.Writer, value *DescriptorKeyError) {
	switch variantValue := value.err.(type) {
	case *DescriptorKeyErrorParse:
		writeInt32(writer, 1)
		FfiConverterStringINSTANCE.Write(writer, variantValue.ErrorMessage)
	case *DescriptorKeyErrorInvalidKeyType:
		writeInt32(writer, 2)
	case *DescriptorKeyErrorBip32:
		writeInt32(writer, 3)
		FfiConverterStringINSTANCE.Write(writer, variantValue.ErrorMessage)
	default:
		_ = variantValue
		panic(fmt.Sprintf("invalid error value `%v` in FfiConverterDescriptorKeyError.Write", value))
	}
}

type FfiDestroyerDescriptorKeyError struct{}

func (_ FfiDestroyerDescriptorKeyError) Destroy(value *DescriptorKeyError) {
	switch variantValue := value.err.(type) {
	case DescriptorKeyErrorParse:
		variantValue.destroy()
	case DescriptorKeyErrorInvalidKeyType:
		variantValue.destroy()
	case DescriptorKeyErrorBip32:
		variantValue.destroy()
	default:
		_ = variantValue
		panic(fmt.Sprintf("invalid error value `%v` in FfiDestroyerDescriptorKeyError.Destroy", value))
	}
}

type ElectrumError struct {
	err error
}

// Convience method to turn *ElectrumError into error
// Avoiding treating nil pointer as non nil error interface
func (err *ElectrumError) AsError() error {
	if err == nil {
		return nil
	} else {
		return err
	}
}

func (err ElectrumError) Error() string {
	return fmt.Sprintf("ElectrumError: %s", err.err.Error())
}

func (err ElectrumError) Unwrap() error {
	return err.err
}

// Err* are used for checking error type with `errors.Is`
var ErrElectrumErrorIoError = fmt.Errorf("ElectrumErrorIoError")
var ErrElectrumErrorJson = fmt.Errorf("ElectrumErrorJson")
var ErrElectrumErrorHex = fmt.Errorf("ElectrumErrorHex")
var ErrElectrumErrorProtocol = fmt.Errorf("ElectrumErrorProtocol")
var ErrElectrumErrorBitcoin = fmt.Errorf("ElectrumErrorBitcoin")
var ErrElectrumErrorAlreadySubscribed = fmt.Errorf("ElectrumErrorAlreadySubscribed")
var ErrElectrumErrorNotSubscribed = fmt.Errorf("ElectrumErrorNotSubscribed")
var ErrElectrumErrorInvalidResponse = fmt.Errorf("ElectrumErrorInvalidResponse")
var ErrElectrumErrorMessage = fmt.Errorf("ElectrumErrorMessage")
var ErrElectrumErrorInvalidDnsNameError = fmt.Errorf("ElectrumErrorInvalidDnsNameError")
var ErrElectrumErrorMissingDomain = fmt.Errorf("ElectrumErrorMissingDomain")
var ErrElectrumErrorAllAttemptsErrored = fmt.Errorf("ElectrumErrorAllAttemptsErrored")
var ErrElectrumErrorSharedIoError = fmt.Errorf("ElectrumErrorSharedIoError")
var ErrElectrumErrorCouldntLockReader = fmt.Errorf("ElectrumErrorCouldntLockReader")
var ErrElectrumErrorMpsc = fmt.Errorf("ElectrumErrorMpsc")
var ErrElectrumErrorCouldNotCreateConnection = fmt.Errorf("ElectrumErrorCouldNotCreateConnection")
var ErrElectrumErrorRequestAlreadyConsumed = fmt.Errorf("ElectrumErrorRequestAlreadyConsumed")

// Variant structs
type ElectrumErrorIoError struct {
	ErrorMessage string
}

func NewElectrumErrorIoError(
	errorMessage string,
) *ElectrumError {
	return &ElectrumError{err: &ElectrumErrorIoError{
		ErrorMessage: errorMessage}}
}

func (e ElectrumErrorIoError) destroy() {
	FfiDestroyerString{}.Destroy(e.ErrorMessage)
}

func (err ElectrumErrorIoError) Error() string {
	return fmt.Sprint("IoError",
		": ",

		"ErrorMessage=",
		err.ErrorMessage,
	)
}

func (self ElectrumErrorIoError) Is(target error) bool {
	return target == ErrElectrumErrorIoError
}

type ElectrumErrorJson struct {
	ErrorMessage string
}

func NewElectrumErrorJson(
	errorMessage string,
) *ElectrumError {
	return &ElectrumError{err: &ElectrumErrorJson{
		ErrorMessage: errorMessage}}
}

func (e ElectrumErrorJson) destroy() {
	FfiDestroyerString{}.Destroy(e.ErrorMessage)
}

func (err ElectrumErrorJson) Error() string {
	return fmt.Sprint("Json",
		": ",

		"ErrorMessage=",
		err.ErrorMessage,
	)
}

func (self ElectrumErrorJson) Is(target error) bool {
	return target == ErrElectrumErrorJson
}

type ElectrumErrorHex struct {
	ErrorMessage string
}

func NewElectrumErrorHex(
	errorMessage string,
) *ElectrumError {
	return &ElectrumError{err: &ElectrumErrorHex{
		ErrorMessage: errorMessage}}
}

func (e ElectrumErrorHex) destroy() {
	FfiDestroyerString{}.Destroy(e.ErrorMessage)
}

func (err ElectrumErrorHex) Error() string {
	return fmt.Sprint("Hex",
		": ",

		"ErrorMessage=",
		err.ErrorMessage,
	)
}

func (self ElectrumErrorHex) Is(target error) bool {
	return target == ErrElectrumErrorHex
}

type ElectrumErrorProtocol struct {
	ErrorMessage string
}

func NewElectrumErrorProtocol(
	errorMessage string,
) *ElectrumError {
	return &ElectrumError{err: &ElectrumErrorProtocol{
		ErrorMessage: errorMessage}}
}

func (e ElectrumErrorProtocol) destroy() {
	FfiDestroyerString{}.Destroy(e.ErrorMessage)
}

func (err ElectrumErrorProtocol) Error() string {
	return fmt.Sprint("Protocol",
		": ",

		"ErrorMessage=",
		err.ErrorMessage,
	)
}

func (self ElectrumErrorProtocol) Is(target error) bool {
	return target == ErrElectrumErrorProtocol
}

type ElectrumErrorBitcoin struct {
	ErrorMessage string
}

func NewElectrumErrorBitcoin(
	errorMessage string,
) *ElectrumError {
	return &ElectrumError{err: &ElectrumErrorBitcoin{
		ErrorMessage: errorMessage}}
}

func (e ElectrumErrorBitcoin) destroy() {
	FfiDestroyerString{}.Destroy(e.ErrorMessage)
}

func (err ElectrumErrorBitcoin) Error() string {
	return fmt.Sprint("Bitcoin",
		": ",

		"ErrorMessage=",
		err.ErrorMessage,
	)
}

func (self ElectrumErrorBitcoin) Is(target error) bool {
	return target == ErrElectrumErrorBitcoin
}

type ElectrumErrorAlreadySubscribed struct {
}

func NewElectrumErrorAlreadySubscribed() *ElectrumError {
	return &ElectrumError{err: &ElectrumErrorAlreadySubscribed{}}
}

func (e ElectrumErrorAlreadySubscribed) destroy() {
}

func (err ElectrumErrorAlreadySubscribed) Error() string {
	return fmt.Sprint("AlreadySubscribed")
}

func (self ElectrumErrorAlreadySubscribed) Is(target error) bool {
	return target == ErrElectrumErrorAlreadySubscribed
}

type ElectrumErrorNotSubscribed struct {
}

func NewElectrumErrorNotSubscribed() *ElectrumError {
	return &ElectrumError{err: &ElectrumErrorNotSubscribed{}}
}

func (e ElectrumErrorNotSubscribed) destroy() {
}

func (err ElectrumErrorNotSubscribed) Error() string {
	return fmt.Sprint("NotSubscribed")
}

func (self ElectrumErrorNotSubscribed) Is(target error) bool {
	return target == ErrElectrumErrorNotSubscribed
}

type ElectrumErrorInvalidResponse struct {
	ErrorMessage string
}

func NewElectrumErrorInvalidResponse(
	errorMessage string,
) *ElectrumError {
	return &ElectrumError{err: &ElectrumErrorInvalidResponse{
		ErrorMessage: errorMessage}}
}

func (e ElectrumErrorInvalidResponse) destroy() {
	FfiDestroyerString{}.Destroy(e.ErrorMessage)
}

func (err ElectrumErrorInvalidResponse) Error() string {
	return fmt.Sprint("InvalidResponse",
		": ",

		"ErrorMessage=",
		err.ErrorMessage,
	)
}

func (self ElectrumErrorInvalidResponse) Is(target error) bool {
	return target == ErrElectrumErrorInvalidResponse
}

type ElectrumErrorMessage struct {
	ErrorMessage string
}

func NewElectrumErrorMessage(
	errorMessage string,
) *ElectrumError {
	return &ElectrumError{err: &ElectrumErrorMessage{
		ErrorMessage: errorMessage}}
}

func (e ElectrumErrorMessage) destroy() {
	FfiDestroyerString{}.Destroy(e.ErrorMessage)
}

func (err ElectrumErrorMessage) Error() string {
	return fmt.Sprint("Message",
		": ",

		"ErrorMessage=",
		err.ErrorMessage,
	)
}

func (self ElectrumErrorMessage) Is(target error) bool {
	return target == ErrElectrumErrorMessage
}

type ElectrumErrorInvalidDnsNameError struct {
	Domain string
}

func NewElectrumErrorInvalidDnsNameError(
	domain string,
) *ElectrumError {
	return &ElectrumError{err: &ElectrumErrorInvalidDnsNameError{
		Domain: domain}}
}

func (e ElectrumErrorInvalidDnsNameError) destroy() {
	FfiDestroyerString{}.Destroy(e.Domain)
}

func (err ElectrumErrorInvalidDnsNameError) Error() string {
	return fmt.Sprint("InvalidDnsNameError",
		": ",

		"Domain=",
		err.Domain,
	)
}

func (self ElectrumErrorInvalidDnsNameError) Is(target error) bool {
	return target == ErrElectrumErrorInvalidDnsNameError
}

type ElectrumErrorMissingDomain struct {
}

func NewElectrumErrorMissingDomain() *ElectrumError {
	return &ElectrumError{err: &ElectrumErrorMissingDomain{}}
}

func (e ElectrumErrorMissingDomain) destroy() {
}

func (err ElectrumErrorMissingDomain) Error() string {
	return fmt.Sprint("MissingDomain")
}

func (self ElectrumErrorMissingDomain) Is(target error) bool {
	return target == ErrElectrumErrorMissingDomain
}

type ElectrumErrorAllAttemptsErrored struct {
}

func NewElectrumErrorAllAttemptsErrored() *ElectrumError {
	return &ElectrumError{err: &ElectrumErrorAllAttemptsErrored{}}
}

func (e ElectrumErrorAllAttemptsErrored) destroy() {
}

func (err ElectrumErrorAllAttemptsErrored) Error() string {
	return fmt.Sprint("AllAttemptsErrored")
}

func (self ElectrumErrorAllAttemptsErrored) Is(target error) bool {
	return target == ErrElectrumErrorAllAttemptsErrored
}

type ElectrumErrorSharedIoError struct {
	ErrorMessage string
}

func NewElectrumErrorSharedIoError(
	errorMessage string,
) *ElectrumError {
	return &ElectrumError{err: &ElectrumErrorSharedIoError{
		ErrorMessage: errorMessage}}
}

func (e ElectrumErrorSharedIoError) destroy() {
	FfiDestroyerString{}.Destroy(e.ErrorMessage)
}

func (err ElectrumErrorSharedIoError) Error() string {
	return fmt.Sprint("SharedIoError",
		": ",

		"ErrorMessage=",
		err.ErrorMessage,
	)
}

func (self ElectrumErrorSharedIoError) Is(target error) bool {
	return target == ErrElectrumErrorSharedIoError
}

type ElectrumErrorCouldntLockReader struct {
}

func NewElectrumErrorCouldntLockReader() *ElectrumError {
	return &ElectrumError{err: &ElectrumErrorCouldntLockReader{}}
}

func (e ElectrumErrorCouldntLockReader) destroy() {
}

func (err ElectrumErrorCouldntLockReader) Error() string {
	return fmt.Sprint("CouldntLockReader")
}

func (self ElectrumErrorCouldntLockReader) Is(target error) bool {
	return target == ErrElectrumErrorCouldntLockReader
}

type ElectrumErrorMpsc struct {
}

func NewElectrumErrorMpsc() *ElectrumError {
	return &ElectrumError{err: &ElectrumErrorMpsc{}}
}

func (e ElectrumErrorMpsc) destroy() {
}

func (err ElectrumErrorMpsc) Error() string {
	return fmt.Sprint("Mpsc")
}

func (self ElectrumErrorMpsc) Is(target error) bool {
	return target == ErrElectrumErrorMpsc
}

type ElectrumErrorCouldNotCreateConnection struct {
	ErrorMessage string
}

func NewElectrumErrorCouldNotCreateConnection(
	errorMessage string,
) *ElectrumError {
	return &ElectrumError{err: &ElectrumErrorCouldNotCreateConnection{
		ErrorMessage: errorMessage}}
}

func (e ElectrumErrorCouldNotCreateConnection) destroy() {
	FfiDestroyerString{}.Destroy(e.ErrorMessage)
}

func (err ElectrumErrorCouldNotCreateConnection) Error() string {
	return fmt.Sprint("CouldNotCreateConnection",
		": ",

		"ErrorMessage=",
		err.ErrorMessage,
	)
}

func (self ElectrumErrorCouldNotCreateConnection) Is(target error) bool {
	return target == ErrElectrumErrorCouldNotCreateConnection
}

type ElectrumErrorRequestAlreadyConsumed struct {
}

func NewElectrumErrorRequestAlreadyConsumed() *ElectrumError {
	return &ElectrumError{err: &ElectrumErrorRequestAlreadyConsumed{}}
}

func (e ElectrumErrorRequestAlreadyConsumed) destroy() {
}

func (err ElectrumErrorRequestAlreadyConsumed) Error() string {
	return fmt.Sprint("RequestAlreadyConsumed")
}

func (self ElectrumErrorRequestAlreadyConsumed) Is(target error) bool {
	return target == ErrElectrumErrorRequestAlreadyConsumed
}

type FfiConverterElectrumError struct{}

var FfiConverterElectrumErrorINSTANCE = FfiConverterElectrumError{}

func (c FfiConverterElectrumError) Lift(eb RustBufferI) *ElectrumError {
	return LiftFromRustBuffer[*ElectrumError](c, eb)
}

func (c FfiConverterElectrumError) Lower(value *ElectrumError) C.RustBuffer {
	return LowerIntoRustBuffer[*ElectrumError](c, value)
}

func (c FfiConverterElectrumError) Read(reader io.Reader) *ElectrumError {
	errorID := readUint32(reader)

	switch errorID {
	case 1:
		return &ElectrumError{&ElectrumErrorIoError{
			ErrorMessage: FfiConverterStringINSTANCE.Read(reader),
		}}
	case 2:
		return &ElectrumError{&ElectrumErrorJson{
			ErrorMessage: FfiConverterStringINSTANCE.Read(reader),
		}}
	case 3:
		return &ElectrumError{&ElectrumErrorHex{
			ErrorMessage: FfiConverterStringINSTANCE.Read(reader),
		}}
	case 4:
		return &ElectrumError{&ElectrumErrorProtocol{
			ErrorMessage: FfiConverterStringINSTANCE.Read(reader),
		}}
	case 5:
		return &ElectrumError{&ElectrumErrorBitcoin{
			ErrorMessage: FfiConverterStringINSTANCE.Read(reader),
		}}
	case 6:
		return &ElectrumError{&ElectrumErrorAlreadySubscribed{}}
	case 7:
		return &ElectrumError{&ElectrumErrorNotSubscribed{}}
	case 8:
		return &ElectrumError{&ElectrumErrorInvalidResponse{
			ErrorMessage: FfiConverterStringINSTANCE.Read(reader),
		}}
	case 9:
		return &ElectrumError{&ElectrumErrorMessage{
			ErrorMessage: FfiConverterStringINSTANCE.Read(reader),
		}}
	case 10:
		return &ElectrumError{&ElectrumErrorInvalidDnsNameError{
			Domain: FfiConverterStringINSTANCE.Read(reader),
		}}
	case 11:
		return &ElectrumError{&ElectrumErrorMissingDomain{}}
	case 12:
		return &ElectrumError{&ElectrumErrorAllAttemptsErrored{}}
	case 13:
		return &ElectrumError{&ElectrumErrorSharedIoError{
			ErrorMessage: FfiConverterStringINSTANCE.Read(reader),
		}}
	case 14:
		return &ElectrumError{&ElectrumErrorCouldntLockReader{}}
	case 15:
		return &ElectrumError{&ElectrumErrorMpsc{}}
	case 16:
		return &ElectrumError{&ElectrumErrorCouldNotCreateConnection{
			ErrorMessage: FfiConverterStringINSTANCE.Read(reader),
		}}
	case 17:
		return &ElectrumError{&ElectrumErrorRequestAlreadyConsumed{}}
	default:
		panic(fmt.Sprintf("Unknown error code %d in FfiConverterElectrumError.Read()", errorID))
	}
}

func (c FfiConverterElectrumError) Write(writer io.Writer, value *ElectrumError) {
	switch variantValue := value.err.(type) {
	case *ElectrumErrorIoError:
		writeInt32(writer, 1)
		FfiConverterStringINSTANCE.Write(writer, variantValue.ErrorMessage)
	case *ElectrumErrorJson:
		writeInt32(writer, 2)
		FfiConverterStringINSTANCE.Write(writer, variantValue.ErrorMessage)
	case *ElectrumErrorHex:
		writeInt32(writer, 3)
		FfiConverterStringINSTANCE.Write(writer, variantValue.ErrorMessage)
	case *ElectrumErrorProtocol:
		writeInt32(writer, 4)
		FfiConverterStringINSTANCE.Write(writer, variantValue.ErrorMessage)
	case *ElectrumErrorBitcoin:
		writeInt32(writer, 5)
		FfiConverterStringINSTANCE.Write(writer, variantValue.ErrorMessage)
	case *ElectrumErrorAlreadySubscribed:
		writeInt32(writer, 6)
	case *ElectrumErrorNotSubscribed:
		writeInt32(writer, 7)
	case *ElectrumErrorInvalidResponse:
		writeInt32(writer, 8)
		FfiConverterStringINSTANCE.Write(writer, variantValue.ErrorMessage)
	case *ElectrumErrorMessage:
		writeInt32(writer, 9)
		FfiConverterStringINSTANCE.Write(writer, variantValue.ErrorMessage)
	case *ElectrumErrorInvalidDnsNameError:
		writeInt32(writer, 10)
		FfiConverterStringINSTANCE.Write(writer, variantValue.Domain)
	case *ElectrumErrorMissingDomain:
		writeInt32(writer, 11)
	case *ElectrumErrorAllAttemptsErrored:
		writeInt32(writer, 12)
	case *ElectrumErrorSharedIoError:
		writeInt32(writer, 13)
		FfiConverterStringINSTANCE.Write(writer, variantValue.ErrorMessage)
	case *ElectrumErrorCouldntLockReader:
		writeInt32(writer, 14)
	case *ElectrumErrorMpsc:
		writeInt32(writer, 15)
	case *ElectrumErrorCouldNotCreateConnection:
		writeInt32(writer, 16)
		FfiConverterStringINSTANCE.Write(writer, variantValue.ErrorMessage)
	case *ElectrumErrorRequestAlreadyConsumed:
		writeInt32(writer, 17)
	default:
		_ = variantValue
		panic(fmt.Sprintf("invalid error value `%v` in FfiConverterElectrumError.Write", value))
	}
}

type FfiDestroyerElectrumError struct{}

func (_ FfiDestroyerElectrumError) Destroy(value *ElectrumError) {
	switch variantValue := value.err.(type) {
	case ElectrumErrorIoError:
		variantValue.destroy()
	case ElectrumErrorJson:
		variantValue.destroy()
	case ElectrumErrorHex:
		variantValue.destroy()
	case ElectrumErrorProtocol:
		variantValue.destroy()
	case ElectrumErrorBitcoin:
		variantValue.destroy()
	case ElectrumErrorAlreadySubscribed:
		variantValue.destroy()
	case ElectrumErrorNotSubscribed:
		variantValue.destroy()
	case ElectrumErrorInvalidResponse:
		variantValue.destroy()
	case ElectrumErrorMessage:
		variantValue.destroy()
	case ElectrumErrorInvalidDnsNameError:
		variantValue.destroy()
	case ElectrumErrorMissingDomain:
		variantValue.destroy()
	case ElectrumErrorAllAttemptsErrored:
		variantValue.destroy()
	case ElectrumErrorSharedIoError:
		variantValue.destroy()
	case ElectrumErrorCouldntLockReader:
		variantValue.destroy()
	case ElectrumErrorMpsc:
		variantValue.destroy()
	case ElectrumErrorCouldNotCreateConnection:
		variantValue.destroy()
	case ElectrumErrorRequestAlreadyConsumed:
		variantValue.destroy()
	default:
		_ = variantValue
		panic(fmt.Sprintf("invalid error value `%v` in FfiDestroyerElectrumError.Destroy", value))
	}
}

type EsploraError struct {
	err error
}

// Convience method to turn *EsploraError into error
// Avoiding treating nil pointer as non nil error interface
func (err *EsploraError) AsError() error {
	if err == nil {
		return nil
	} else {
		return err
	}
}

func (err EsploraError) Error() string {
	return fmt.Sprintf("EsploraError: %s", err.err.Error())
}

func (err EsploraError) Unwrap() error {
	return err.err
}

// Err* are used for checking error type with `errors.Is`
var ErrEsploraErrorMinreq = fmt.Errorf("EsploraErrorMinreq")
var ErrEsploraErrorHttpResponse = fmt.Errorf("EsploraErrorHttpResponse")
var ErrEsploraErrorParsing = fmt.Errorf("EsploraErrorParsing")
var ErrEsploraErrorStatusCode = fmt.Errorf("EsploraErrorStatusCode")
var ErrEsploraErrorBitcoinEncoding = fmt.Errorf("EsploraErrorBitcoinEncoding")
var ErrEsploraErrorHexToArray = fmt.Errorf("EsploraErrorHexToArray")
var ErrEsploraErrorHexToBytes = fmt.Errorf("EsploraErrorHexToBytes")
var ErrEsploraErrorTransactionNotFound = fmt.Errorf("EsploraErrorTransactionNotFound")
var ErrEsploraErrorHeaderHeightNotFound = fmt.Errorf("EsploraErrorHeaderHeightNotFound")
var ErrEsploraErrorHeaderHashNotFound = fmt.Errorf("EsploraErrorHeaderHashNotFound")
var ErrEsploraErrorInvalidHttpHeaderName = fmt.Errorf("EsploraErrorInvalidHttpHeaderName")
var ErrEsploraErrorInvalidHttpHeaderValue = fmt.Errorf("EsploraErrorInvalidHttpHeaderValue")
var ErrEsploraErrorRequestAlreadyConsumed = fmt.Errorf("EsploraErrorRequestAlreadyConsumed")
var ErrEsploraErrorInvalidResponse = fmt.Errorf("EsploraErrorInvalidResponse")

// Variant structs
type EsploraErrorMinreq struct {
	ErrorMessage string
}

func NewEsploraErrorMinreq(
	errorMessage string,
) *EsploraError {
	return &EsploraError{err: &EsploraErrorMinreq{
		ErrorMessage: errorMessage}}
}

func (e EsploraErrorMinreq) destroy() {
	FfiDestroyerString{}.Destroy(e.ErrorMessage)
}

func (err EsploraErrorMinreq) Error() string {
	return fmt.Sprint("Minreq",
		": ",

		"ErrorMessage=",
		err.ErrorMessage,
	)
}

func (self EsploraErrorMinreq) Is(target error) bool {
	return target == ErrEsploraErrorMinreq
}

type EsploraErrorHttpResponse struct {
	Status       uint16
	ErrorMessage string
}

func NewEsploraErrorHttpResponse(
	status uint16,
	errorMessage string,
) *EsploraError {
	return &EsploraError{err: &EsploraErrorHttpResponse{
		Status:       status,
		ErrorMessage: errorMessage}}
}

func (e EsploraErrorHttpResponse) destroy() {
	FfiDestroyerUint16{}.Destroy(e.Status)
	FfiDestroyerString{}.Destroy(e.ErrorMessage)
}

func (err EsploraErrorHttpResponse) Error() string {
	return fmt.Sprint("HttpResponse",
		": ",

		"Status=",
		err.Status,
		", ",
		"ErrorMessage=",
		err.ErrorMessage,
	)
}

func (self EsploraErrorHttpResponse) Is(target error) bool {
	return target == ErrEsploraErrorHttpResponse
}

type EsploraErrorParsing struct {
	ErrorMessage string
}

func NewEsploraErrorParsing(
	errorMessage string,
) *EsploraError {
	return &EsploraError{err: &EsploraErrorParsing{
		ErrorMessage: errorMessage}}
}

func (e EsploraErrorParsing) destroy() {
	FfiDestroyerString{}.Destroy(e.ErrorMessage)
}

func (err EsploraErrorParsing) Error() string {
	return fmt.Sprint("Parsing",
		": ",

		"ErrorMessage=",
		err.ErrorMessage,
	)
}

func (self EsploraErrorParsing) Is(target error) bool {
	return target == ErrEsploraErrorParsing
}

type EsploraErrorStatusCode struct {
	ErrorMessage string
}

func NewEsploraErrorStatusCode(
	errorMessage string,
) *EsploraError {
	return &EsploraError{err: &EsploraErrorStatusCode{
		ErrorMessage: errorMessage}}
}

func (e EsploraErrorStatusCode) destroy() {
	FfiDestroyerString{}.Destroy(e.ErrorMessage)
}

func (err EsploraErrorStatusCode) Error() string {
	return fmt.Sprint("StatusCode",
		": ",

		"ErrorMessage=",
		err.ErrorMessage,
	)
}

func (self EsploraErrorStatusCode) Is(target error) bool {
	return target == ErrEsploraErrorStatusCode
}

type EsploraErrorBitcoinEncoding struct {
	ErrorMessage string
}

func NewEsploraErrorBitcoinEncoding(
	errorMessage string,
) *EsploraError {
	return &EsploraError{err: &EsploraErrorBitcoinEncoding{
		ErrorMessage: errorMessage}}
}

func (e EsploraErrorBitcoinEncoding) destroy() {
	FfiDestroyerString{}.Destroy(e.ErrorMessage)
}

func (err EsploraErrorBitcoinEncoding) Error() string {
	return fmt.Sprint("BitcoinEncoding",
		": ",

		"ErrorMessage=",
		err.ErrorMessage,
	)
}

func (self EsploraErrorBitcoinEncoding) Is(target error) bool {
	return target == ErrEsploraErrorBitcoinEncoding
}

type EsploraErrorHexToArray struct {
	ErrorMessage string
}

func NewEsploraErrorHexToArray(
	errorMessage string,
) *EsploraError {
	return &EsploraError{err: &EsploraErrorHexToArray{
		ErrorMessage: errorMessage}}
}

func (e EsploraErrorHexToArray) destroy() {
	FfiDestroyerString{}.Destroy(e.ErrorMessage)
}

func (err EsploraErrorHexToArray) Error() string {
	return fmt.Sprint("HexToArray",
		": ",

		"ErrorMessage=",
		err.ErrorMessage,
	)
}

func (self EsploraErrorHexToArray) Is(target error) bool {
	return target == ErrEsploraErrorHexToArray
}

type EsploraErrorHexToBytes struct {
	ErrorMessage string
}

func NewEsploraErrorHexToBytes(
	errorMessage string,
) *EsploraError {
	return &EsploraError{err: &EsploraErrorHexToBytes{
		ErrorMessage: errorMessage}}
}

func (e EsploraErrorHexToBytes) destroy() {
	FfiDestroyerString{}.Destroy(e.ErrorMessage)
}

func (err EsploraErrorHexToBytes) Error() string {
	return fmt.Sprint("HexToBytes",
		": ",

		"ErrorMessage=",
		err.ErrorMessage,
	)
}

func (self EsploraErrorHexToBytes) Is(target error) bool {
	return target == ErrEsploraErrorHexToBytes
}

type EsploraErrorTransactionNotFound struct {
}

func NewEsploraErrorTransactionNotFound() *EsploraError {
	return &EsploraError{err: &EsploraErrorTransactionNotFound{}}
}

func (e EsploraErrorTransactionNotFound) destroy() {
}

func (err EsploraErrorTransactionNotFound) Error() string {
	return fmt.Sprint("TransactionNotFound")
}

func (self EsploraErrorTransactionNotFound) Is(target error) bool {
	return target == ErrEsploraErrorTransactionNotFound
}

type EsploraErrorHeaderHeightNotFound struct {
	Height uint32
}

func NewEsploraErrorHeaderHeightNotFound(
	height uint32,
) *EsploraError {
	return &EsploraError{err: &EsploraErrorHeaderHeightNotFound{
		Height: height}}
}

func (e EsploraErrorHeaderHeightNotFound) destroy() {
	FfiDestroyerUint32{}.Destroy(e.Height)
}

func (err EsploraErrorHeaderHeightNotFound) Error() string {
	return fmt.Sprint("HeaderHeightNotFound",
		": ",

		"Height=",
		err.Height,
	)
}

func (self EsploraErrorHeaderHeightNotFound) Is(target error) bool {
	return target == ErrEsploraErrorHeaderHeightNotFound
}

type EsploraErrorHeaderHashNotFound struct {
}

func NewEsploraErrorHeaderHashNotFound() *EsploraError {
	return &EsploraError{err: &EsploraErrorHeaderHashNotFound{}}
}

func (e EsploraErrorHeaderHashNotFound) destroy() {
}

func (err EsploraErrorHeaderHashNotFound) Error() string {
	return fmt.Sprint("HeaderHashNotFound")
}

func (self EsploraErrorHeaderHashNotFound) Is(target error) bool {
	return target == ErrEsploraErrorHeaderHashNotFound
}

type EsploraErrorInvalidHttpHeaderName struct {
	Name string
}

func NewEsploraErrorInvalidHttpHeaderName(
	name string,
) *EsploraError {
	return &EsploraError{err: &EsploraErrorInvalidHttpHeaderName{
		Name: name}}
}

func (e EsploraErrorInvalidHttpHeaderName) destroy() {
	FfiDestroyerString{}.Destroy(e.Name)
}

func (err EsploraErrorInvalidHttpHeaderName) Error() string {
	return fmt.Sprint("InvalidHttpHeaderName",
		": ",

		"Name=",
		err.Name,
	)
}

func (self EsploraErrorInvalidHttpHeaderName) Is(target error) bool {
	return target == ErrEsploraErrorInvalidHttpHeaderName
}

type EsploraErrorInvalidHttpHeaderValue struct {
	Value string
}

func NewEsploraErrorInvalidHttpHeaderValue(
	value string,
) *EsploraError {
	return &EsploraError{err: &EsploraErrorInvalidHttpHeaderValue{
		Value: value}}
}

func (e EsploraErrorInvalidHttpHeaderValue) destroy() {
	FfiDestroyerString{}.Destroy(e.Value)
}

func (err EsploraErrorInvalidHttpHeaderValue) Error() string {
	return fmt.Sprint("InvalidHttpHeaderValue",
		": ",

		"Value=",
		err.Value,
	)
}

func (self EsploraErrorInvalidHttpHeaderValue) Is(target error) bool {
	return target == ErrEsploraErrorInvalidHttpHeaderValue
}

type EsploraErrorRequestAlreadyConsumed struct {
}

func NewEsploraErrorRequestAlreadyConsumed() *EsploraError {
	return &EsploraError{err: &EsploraErrorRequestAlreadyConsumed{}}
}

func (e EsploraErrorRequestAlreadyConsumed) destroy() {
}

func (err EsploraErrorRequestAlreadyConsumed) Error() string {
	return fmt.Sprint("RequestAlreadyConsumed")
}

func (self EsploraErrorRequestAlreadyConsumed) Is(target error) bool {
	return target == ErrEsploraErrorRequestAlreadyConsumed
}

type EsploraErrorInvalidResponse struct {
}

func NewEsploraErrorInvalidResponse() *EsploraError {
	return &EsploraError{err: &EsploraErrorInvalidResponse{}}
}

func (e EsploraErrorInvalidResponse) destroy() {
}

func (err EsploraErrorInvalidResponse) Error() string {
	return fmt.Sprint("InvalidResponse")
}

func (self EsploraErrorInvalidResponse) Is(target error) bool {
	return target == ErrEsploraErrorInvalidResponse
}

type FfiConverterEsploraError struct{}

var FfiConverterEsploraErrorINSTANCE = FfiConverterEsploraError{}

func (c FfiConverterEsploraError) Lift(eb RustBufferI) *EsploraError {
	return LiftFromRustBuffer[*EsploraError](c, eb)
}

func (c FfiConverterEsploraError) Lower(value *EsploraError) C.RustBuffer {
	return LowerIntoRustBuffer[*EsploraError](c, value)
}

func (c FfiConverterEsploraError) Read(reader io.Reader) *EsploraError {
	errorID := readUint32(reader)

	switch errorID {
	case 1:
		return &EsploraError{&EsploraErrorMinreq{
			ErrorMessage: FfiConverterStringINSTANCE.Read(reader),
		}}
	case 2:
		return &EsploraError{&EsploraErrorHttpResponse{
			Status:       FfiConverterUint16INSTANCE.Read(reader),
			ErrorMessage: FfiConverterStringINSTANCE.Read(reader),
		}}
	case 3:
		return &EsploraError{&EsploraErrorParsing{
			ErrorMessage: FfiConverterStringINSTANCE.Read(reader),
		}}
	case 4:
		return &EsploraError{&EsploraErrorStatusCode{
			ErrorMessage: FfiConverterStringINSTANCE.Read(reader),
		}}
	case 5:
		return &EsploraError{&EsploraErrorBitcoinEncoding{
			ErrorMessage: FfiConverterStringINSTANCE.Read(reader),
		}}
	case 6:
		return &EsploraError{&EsploraErrorHexToArray{
			ErrorMessage: FfiConverterStringINSTANCE.Read(reader),
		}}
	case 7:
		return &EsploraError{&EsploraErrorHexToBytes{
			ErrorMessage: FfiConverterStringINSTANCE.Read(reader),
		}}
	case 8:
		return &EsploraError{&EsploraErrorTransactionNotFound{}}
	case 9:
		return &EsploraError{&EsploraErrorHeaderHeightNotFound{
			Height: FfiConverterUint32INSTANCE.Read(reader),
		}}
	case 10:
		return &EsploraError{&EsploraErrorHeaderHashNotFound{}}
	case 11:
		return &EsploraError{&EsploraErrorInvalidHttpHeaderName{
			Name: FfiConverterStringINSTANCE.Read(reader),
		}}
	case 12:
		return &EsploraError{&EsploraErrorInvalidHttpHeaderValue{
			Value: FfiConverterStringINSTANCE.Read(reader),
		}}
	case 13:
		return &EsploraError{&EsploraErrorRequestAlreadyConsumed{}}
	case 14:
		return &EsploraError{&EsploraErrorInvalidResponse{}}
	default:
		panic(fmt.Sprintf("Unknown error code %d in FfiConverterEsploraError.Read()", errorID))
	}
}

func (c FfiConverterEsploraError) Write(writer io.Writer, value *EsploraError) {
	switch variantValue := value.err.(type) {
	case *EsploraErrorMinreq:
		writeInt32(writer, 1)
		FfiConverterStringINSTANCE.Write(writer, variantValue.ErrorMessage)
	case *EsploraErrorHttpResponse:
		writeInt32(writer, 2)
		FfiConverterUint16INSTANCE.Write(writer, variantValue.Status)
		FfiConverterStringINSTANCE.Write(writer, variantValue.ErrorMessage)
	case *EsploraErrorParsing:
		writeInt32(writer, 3)
		FfiConverterStringINSTANCE.Write(writer, variantValue.ErrorMessage)
	case *EsploraErrorStatusCode:
		writeInt32(writer, 4)
		FfiConverterStringINSTANCE.Write(writer, variantValue.ErrorMessage)
	case *EsploraErrorBitcoinEncoding:
		writeInt32(writer, 5)
		FfiConverterStringINSTANCE.Write(writer, variantValue.ErrorMessage)
	case *EsploraErrorHexToArray:
		writeInt32(writer, 6)
		FfiConverterStringINSTANCE.Write(writer, variantValue.ErrorMessage)
	case *EsploraErrorHexToBytes:
		writeInt32(writer, 7)
		FfiConverterStringINSTANCE.Write(writer, variantValue.ErrorMessage)
	case *EsploraErrorTransactionNotFound:
		writeInt32(writer, 8)
	case *EsploraErrorHeaderHeightNotFound:
		writeInt32(writer, 9)
		FfiConverterUint32INSTANCE.Write(writer, variantValue.Height)
	case *EsploraErrorHeaderHashNotFound:
		writeInt32(writer, 10)
	case *EsploraErrorInvalidHttpHeaderName:
		writeInt32(writer, 11)
		FfiConverterStringINSTANCE.Write(writer, variantValue.Name)
	case *EsploraErrorInvalidHttpHeaderValue:
		writeInt32(writer, 12)
		FfiConverterStringINSTANCE.Write(writer, variantValue.Value)
	case *EsploraErrorRequestAlreadyConsumed:
		writeInt32(writer, 13)
	case *EsploraErrorInvalidResponse:
		writeInt32(writer, 14)
	default:
		_ = variantValue
		panic(fmt.Sprintf("invalid error value `%v` in FfiConverterEsploraError.Write", value))
	}
}

type FfiDestroyerEsploraError struct{}

func (_ FfiDestroyerEsploraError) Destroy(value *EsploraError) {
	switch variantValue := value.err.(type) {
	case EsploraErrorMinreq:
		variantValue.destroy()
	case EsploraErrorHttpResponse:
		variantValue.destroy()
	case EsploraErrorParsing:
		variantValue.destroy()
	case EsploraErrorStatusCode:
		variantValue.destroy()
	case EsploraErrorBitcoinEncoding:
		variantValue.destroy()
	case EsploraErrorHexToArray:
		variantValue.destroy()
	case EsploraErrorHexToBytes:
		variantValue.destroy()
	case EsploraErrorTransactionNotFound:
		variantValue.destroy()
	case EsploraErrorHeaderHeightNotFound:
		variantValue.destroy()
	case EsploraErrorHeaderHashNotFound:
		variantValue.destroy()
	case EsploraErrorInvalidHttpHeaderName:
		variantValue.destroy()
	case EsploraErrorInvalidHttpHeaderValue:
		variantValue.destroy()
	case EsploraErrorRequestAlreadyConsumed:
		variantValue.destroy()
	case EsploraErrorInvalidResponse:
		variantValue.destroy()
	default:
		_ = variantValue
		panic(fmt.Sprintf("invalid error value `%v` in FfiDestroyerEsploraError.Destroy", value))
	}
}

type ExtractTxError struct {
	err error
}

// Convience method to turn *ExtractTxError into error
// Avoiding treating nil pointer as non nil error interface
func (err *ExtractTxError) AsError() error {
	if err == nil {
		return nil
	} else {
		return err
	}
}

func (err ExtractTxError) Error() string {
	return fmt.Sprintf("ExtractTxError: %s", err.err.Error())
}

func (err ExtractTxError) Unwrap() error {
	return err.err
}

// Err* are used for checking error type with `errors.Is`
var ErrExtractTxErrorAbsurdFeeRate = fmt.Errorf("ExtractTxErrorAbsurdFeeRate")
var ErrExtractTxErrorMissingInputValue = fmt.Errorf("ExtractTxErrorMissingInputValue")
var ErrExtractTxErrorSendingTooMuch = fmt.Errorf("ExtractTxErrorSendingTooMuch")
var ErrExtractTxErrorOtherExtractTxErr = fmt.Errorf("ExtractTxErrorOtherExtractTxErr")

// Variant structs
type ExtractTxErrorAbsurdFeeRate struct {
	FeeRate uint64
}

func NewExtractTxErrorAbsurdFeeRate(
	feeRate uint64,
) *ExtractTxError {
	return &ExtractTxError{err: &ExtractTxErrorAbsurdFeeRate{
		FeeRate: feeRate}}
}

func (e ExtractTxErrorAbsurdFeeRate) destroy() {
	FfiDestroyerUint64{}.Destroy(e.FeeRate)
}

func (err ExtractTxErrorAbsurdFeeRate) Error() string {
	return fmt.Sprint("AbsurdFeeRate",
		": ",

		"FeeRate=",
		err.FeeRate,
	)
}

func (self ExtractTxErrorAbsurdFeeRate) Is(target error) bool {
	return target == ErrExtractTxErrorAbsurdFeeRate
}

type ExtractTxErrorMissingInputValue struct {
}

func NewExtractTxErrorMissingInputValue() *ExtractTxError {
	return &ExtractTxError{err: &ExtractTxErrorMissingInputValue{}}
}

func (e ExtractTxErrorMissingInputValue) destroy() {
}

func (err ExtractTxErrorMissingInputValue) Error() string {
	return fmt.Sprint("MissingInputValue")
}

func (self ExtractTxErrorMissingInputValue) Is(target error) bool {
	return target == ErrExtractTxErrorMissingInputValue
}

type ExtractTxErrorSendingTooMuch struct {
}

func NewExtractTxErrorSendingTooMuch() *ExtractTxError {
	return &ExtractTxError{err: &ExtractTxErrorSendingTooMuch{}}
}

func (e ExtractTxErrorSendingTooMuch) destroy() {
}

func (err ExtractTxErrorSendingTooMuch) Error() string {
	return fmt.Sprint("SendingTooMuch")
}

func (self ExtractTxErrorSendingTooMuch) Is(target error) bool {
	return target == ErrExtractTxErrorSendingTooMuch
}

type ExtractTxErrorOtherExtractTxErr struct {
}

func NewExtractTxErrorOtherExtractTxErr() *ExtractTxError {
	return &ExtractTxError{err: &ExtractTxErrorOtherExtractTxErr{}}
}

func (e ExtractTxErrorOtherExtractTxErr) destroy() {
}

func (err ExtractTxErrorOtherExtractTxErr) Error() string {
	return fmt.Sprint("OtherExtractTxErr")
}

func (self ExtractTxErrorOtherExtractTxErr) Is(target error) bool {
	return target == ErrExtractTxErrorOtherExtractTxErr
}

type FfiConverterExtractTxError struct{}

var FfiConverterExtractTxErrorINSTANCE = FfiConverterExtractTxError{}

func (c FfiConverterExtractTxError) Lift(eb RustBufferI) *ExtractTxError {
	return LiftFromRustBuffer[*ExtractTxError](c, eb)
}

func (c FfiConverterExtractTxError) Lower(value *ExtractTxError) C.RustBuffer {
	return LowerIntoRustBuffer[*ExtractTxError](c, value)
}

func (c FfiConverterExtractTxError) Read(reader io.Reader) *ExtractTxError {
	errorID := readUint32(reader)

	switch errorID {
	case 1:
		return &ExtractTxError{&ExtractTxErrorAbsurdFeeRate{
			FeeRate: FfiConverterUint64INSTANCE.Read(reader),
		}}
	case 2:
		return &ExtractTxError{&ExtractTxErrorMissingInputValue{}}
	case 3:
		return &ExtractTxError{&ExtractTxErrorSendingTooMuch{}}
	case 4:
		return &ExtractTxError{&ExtractTxErrorOtherExtractTxErr{}}
	default:
		panic(fmt.Sprintf("Unknown error code %d in FfiConverterExtractTxError.Read()", errorID))
	}
}

func (c FfiConverterExtractTxError) Write(writer io.Writer, value *ExtractTxError) {
	switch variantValue := value.err.(type) {
	case *ExtractTxErrorAbsurdFeeRate:
		writeInt32(writer, 1)
		FfiConverterUint64INSTANCE.Write(writer, variantValue.FeeRate)
	case *ExtractTxErrorMissingInputValue:
		writeInt32(writer, 2)
	case *ExtractTxErrorSendingTooMuch:
		writeInt32(writer, 3)
	case *ExtractTxErrorOtherExtractTxErr:
		writeInt32(writer, 4)
	default:
		_ = variantValue
		panic(fmt.Sprintf("invalid error value `%v` in FfiConverterExtractTxError.Write", value))
	}
}

type FfiDestroyerExtractTxError struct{}

func (_ FfiDestroyerExtractTxError) Destroy(value *ExtractTxError) {
	switch variantValue := value.err.(type) {
	case ExtractTxErrorAbsurdFeeRate:
		variantValue.destroy()
	case ExtractTxErrorMissingInputValue:
		variantValue.destroy()
	case ExtractTxErrorSendingTooMuch:
		variantValue.destroy()
	case ExtractTxErrorOtherExtractTxErr:
		variantValue.destroy()
	default:
		_ = variantValue
		panic(fmt.Sprintf("invalid error value `%v` in FfiDestroyerExtractTxError.Destroy", value))
	}
}

type FeeRateError struct {
	err error
}

// Convience method to turn *FeeRateError into error
// Avoiding treating nil pointer as non nil error interface
func (err *FeeRateError) AsError() error {
	if err == nil {
		return nil
	} else {
		return err
	}
}

func (err FeeRateError) Error() string {
	return fmt.Sprintf("FeeRateError: %s", err.err.Error())
}

func (err FeeRateError) Unwrap() error {
	return err.err
}

// Err* are used for checking error type with `errors.Is`
var ErrFeeRateErrorArithmeticOverflow = fmt.Errorf("FeeRateErrorArithmeticOverflow")

// Variant structs
type FeeRateErrorArithmeticOverflow struct {
}

func NewFeeRateErrorArithmeticOverflow() *FeeRateError {
	return &FeeRateError{err: &FeeRateErrorArithmeticOverflow{}}
}

func (e FeeRateErrorArithmeticOverflow) destroy() {
}

func (err FeeRateErrorArithmeticOverflow) Error() string {
	return fmt.Sprint("ArithmeticOverflow")
}

func (self FeeRateErrorArithmeticOverflow) Is(target error) bool {
	return target == ErrFeeRateErrorArithmeticOverflow
}

type FfiConverterFeeRateError struct{}

var FfiConverterFeeRateErrorINSTANCE = FfiConverterFeeRateError{}

func (c FfiConverterFeeRateError) Lift(eb RustBufferI) *FeeRateError {
	return LiftFromRustBuffer[*FeeRateError](c, eb)
}

func (c FfiConverterFeeRateError) Lower(value *FeeRateError) C.RustBuffer {
	return LowerIntoRustBuffer[*FeeRateError](c, value)
}

func (c FfiConverterFeeRateError) Read(reader io.Reader) *FeeRateError {
	errorID := readUint32(reader)

	switch errorID {
	case 1:
		return &FeeRateError{&FeeRateErrorArithmeticOverflow{}}
	default:
		panic(fmt.Sprintf("Unknown error code %d in FfiConverterFeeRateError.Read()", errorID))
	}
}

func (c FfiConverterFeeRateError) Write(writer io.Writer, value *FeeRateError) {
	switch variantValue := value.err.(type) {
	case *FeeRateErrorArithmeticOverflow:
		writeInt32(writer, 1)
	default:
		_ = variantValue
		panic(fmt.Sprintf("invalid error value `%v` in FfiConverterFeeRateError.Write", value))
	}
}

type FfiDestroyerFeeRateError struct{}

func (_ FfiDestroyerFeeRateError) Destroy(value *FeeRateError) {
	switch variantValue := value.err.(type) {
	case FeeRateErrorArithmeticOverflow:
		variantValue.destroy()
	default:
		_ = variantValue
		panic(fmt.Sprintf("invalid error value `%v` in FfiDestroyerFeeRateError.Destroy", value))
	}
}

type FromScriptError struct {
	err error
}

// Convience method to turn *FromScriptError into error
// Avoiding treating nil pointer as non nil error interface
func (err *FromScriptError) AsError() error {
	if err == nil {
		return nil
	} else {
		return err
	}
}

func (err FromScriptError) Error() string {
	return fmt.Sprintf("FromScriptError: %s", err.err.Error())
}

func (err FromScriptError) Unwrap() error {
	return err.err
}

// Err* are used for checking error type with `errors.Is`
var ErrFromScriptErrorUnrecognizedScript = fmt.Errorf("FromScriptErrorUnrecognizedScript")
var ErrFromScriptErrorWitnessProgram = fmt.Errorf("FromScriptErrorWitnessProgram")
var ErrFromScriptErrorWitnessVersion = fmt.Errorf("FromScriptErrorWitnessVersion")
var ErrFromScriptErrorOtherFromScriptErr = fmt.Errorf("FromScriptErrorOtherFromScriptErr")

// Variant structs
type FromScriptErrorUnrecognizedScript struct {
}

func NewFromScriptErrorUnrecognizedScript() *FromScriptError {
	return &FromScriptError{err: &FromScriptErrorUnrecognizedScript{}}
}

func (e FromScriptErrorUnrecognizedScript) destroy() {
}

func (err FromScriptErrorUnrecognizedScript) Error() string {
	return fmt.Sprint("UnrecognizedScript")
}

func (self FromScriptErrorUnrecognizedScript) Is(target error) bool {
	return target == ErrFromScriptErrorUnrecognizedScript
}

type FromScriptErrorWitnessProgram struct {
	ErrorMessage string
}

func NewFromScriptErrorWitnessProgram(
	errorMessage string,
) *FromScriptError {
	return &FromScriptError{err: &FromScriptErrorWitnessProgram{
		ErrorMessage: errorMessage}}
}

func (e FromScriptErrorWitnessProgram) destroy() {
	FfiDestroyerString{}.Destroy(e.ErrorMessage)
}

func (err FromScriptErrorWitnessProgram) Error() string {
	return fmt.Sprint("WitnessProgram",
		": ",

		"ErrorMessage=",
		err.ErrorMessage,
	)
}

func (self FromScriptErrorWitnessProgram) Is(target error) bool {
	return target == ErrFromScriptErrorWitnessProgram
}

type FromScriptErrorWitnessVersion struct {
	ErrorMessage string
}

func NewFromScriptErrorWitnessVersion(
	errorMessage string,
) *FromScriptError {
	return &FromScriptError{err: &FromScriptErrorWitnessVersion{
		ErrorMessage: errorMessage}}
}

func (e FromScriptErrorWitnessVersion) destroy() {
	FfiDestroyerString{}.Destroy(e.ErrorMessage)
}

func (err FromScriptErrorWitnessVersion) Error() string {
	return fmt.Sprint("WitnessVersion",
		": ",

		"ErrorMessage=",
		err.ErrorMessage,
	)
}

func (self FromScriptErrorWitnessVersion) Is(target error) bool {
	return target == ErrFromScriptErrorWitnessVersion
}

type FromScriptErrorOtherFromScriptErr struct {
}

func NewFromScriptErrorOtherFromScriptErr() *FromScriptError {
	return &FromScriptError{err: &FromScriptErrorOtherFromScriptErr{}}
}

func (e FromScriptErrorOtherFromScriptErr) destroy() {
}

func (err FromScriptErrorOtherFromScriptErr) Error() string {
	return fmt.Sprint("OtherFromScriptErr")
}

func (self FromScriptErrorOtherFromScriptErr) Is(target error) bool {
	return target == ErrFromScriptErrorOtherFromScriptErr
}

type FfiConverterFromScriptError struct{}

var FfiConverterFromScriptErrorINSTANCE = FfiConverterFromScriptError{}

func (c FfiConverterFromScriptError) Lift(eb RustBufferI) *FromScriptError {
	return LiftFromRustBuffer[*FromScriptError](c, eb)
}

func (c FfiConverterFromScriptError) Lower(value *FromScriptError) C.RustBuffer {
	return LowerIntoRustBuffer[*FromScriptError](c, value)
}

func (c FfiConverterFromScriptError) Read(reader io.Reader) *FromScriptError {
	errorID := readUint32(reader)

	switch errorID {
	case 1:
		return &FromScriptError{&FromScriptErrorUnrecognizedScript{}}
	case 2:
		return &FromScriptError{&FromScriptErrorWitnessProgram{
			ErrorMessage: FfiConverterStringINSTANCE.Read(reader),
		}}
	case 3:
		return &FromScriptError{&FromScriptErrorWitnessVersion{
			ErrorMessage: FfiConverterStringINSTANCE.Read(reader),
		}}
	case 4:
		return &FromScriptError{&FromScriptErrorOtherFromScriptErr{}}
	default:
		panic(fmt.Sprintf("Unknown error code %d in FfiConverterFromScriptError.Read()", errorID))
	}
}

func (c FfiConverterFromScriptError) Write(writer io.Writer, value *FromScriptError) {
	switch variantValue := value.err.(type) {
	case *FromScriptErrorUnrecognizedScript:
		writeInt32(writer, 1)
	case *FromScriptErrorWitnessProgram:
		writeInt32(writer, 2)
		FfiConverterStringINSTANCE.Write(writer, variantValue.ErrorMessage)
	case *FromScriptErrorWitnessVersion:
		writeInt32(writer, 3)
		FfiConverterStringINSTANCE.Write(writer, variantValue.ErrorMessage)
	case *FromScriptErrorOtherFromScriptErr:
		writeInt32(writer, 4)
	default:
		_ = variantValue
		panic(fmt.Sprintf("invalid error value `%v` in FfiConverterFromScriptError.Write", value))
	}
}

type FfiDestroyerFromScriptError struct{}

func (_ FfiDestroyerFromScriptError) Destroy(value *FromScriptError) {
	switch variantValue := value.err.(type) {
	case FromScriptErrorUnrecognizedScript:
		variantValue.destroy()
	case FromScriptErrorWitnessProgram:
		variantValue.destroy()
	case FromScriptErrorWitnessVersion:
		variantValue.destroy()
	case FromScriptErrorOtherFromScriptErr:
		variantValue.destroy()
	default:
		_ = variantValue
		panic(fmt.Sprintf("invalid error value `%v` in FfiDestroyerFromScriptError.Destroy", value))
	}
}

// Types of keychains
type KeychainKind uint

const (
	// External keychain, used for deriving recipient addresses.
	KeychainKindExternal KeychainKind = 1
	// Internal keychain, used for deriving change addresses.
	KeychainKindInternal KeychainKind = 2
)

type FfiConverterKeychainKind struct{}

var FfiConverterKeychainKindINSTANCE = FfiConverterKeychainKind{}

func (c FfiConverterKeychainKind) Lift(rb RustBufferI) KeychainKind {
	return LiftFromRustBuffer[KeychainKind](c, rb)
}

func (c FfiConverterKeychainKind) Lower(value KeychainKind) C.RustBuffer {
	return LowerIntoRustBuffer[KeychainKind](c, value)
}
func (FfiConverterKeychainKind) Read(reader io.Reader) KeychainKind {
	id := readInt32(reader)
	return KeychainKind(id)
}

func (FfiConverterKeychainKind) Write(writer io.Writer, value KeychainKind) {
	writeInt32(writer, int32(value))
}

type FfiDestroyerKeychainKind struct{}

func (_ FfiDestroyerKeychainKind) Destroy(value KeychainKind) {
}

type LoadWithPersistError struct {
	err error
}

// Convience method to turn *LoadWithPersistError into error
// Avoiding treating nil pointer as non nil error interface
func (err *LoadWithPersistError) AsError() error {
	if err == nil {
		return nil
	} else {
		return err
	}
}

func (err LoadWithPersistError) Error() string {
	return fmt.Sprintf("LoadWithPersistError: %s", err.err.Error())
}

func (err LoadWithPersistError) Unwrap() error {
	return err.err
}

// Err* are used for checking error type with `errors.Is`
var ErrLoadWithPersistErrorPersist = fmt.Errorf("LoadWithPersistErrorPersist")
var ErrLoadWithPersistErrorInvalidChangeSet = fmt.Errorf("LoadWithPersistErrorInvalidChangeSet")
var ErrLoadWithPersistErrorCouldNotLoad = fmt.Errorf("LoadWithPersistErrorCouldNotLoad")

// Variant structs
type LoadWithPersistErrorPersist struct {
	ErrorMessage string
}

func NewLoadWithPersistErrorPersist(
	errorMessage string,
) *LoadWithPersistError {
	return &LoadWithPersistError{err: &LoadWithPersistErrorPersist{
		ErrorMessage: errorMessage}}
}

func (e LoadWithPersistErrorPersist) destroy() {
	FfiDestroyerString{}.Destroy(e.ErrorMessage)
}

func (err LoadWithPersistErrorPersist) Error() string {
	return fmt.Sprint("Persist",
		": ",

		"ErrorMessage=",
		err.ErrorMessage,
	)
}

func (self LoadWithPersistErrorPersist) Is(target error) bool {
	return target == ErrLoadWithPersistErrorPersist
}

type LoadWithPersistErrorInvalidChangeSet struct {
	ErrorMessage string
}

func NewLoadWithPersistErrorInvalidChangeSet(
	errorMessage string,
) *LoadWithPersistError {
	return &LoadWithPersistError{err: &LoadWithPersistErrorInvalidChangeSet{
		ErrorMessage: errorMessage}}
}

func (e LoadWithPersistErrorInvalidChangeSet) destroy() {
	FfiDestroyerString{}.Destroy(e.ErrorMessage)
}

func (err LoadWithPersistErrorInvalidChangeSet) Error() string {
	return fmt.Sprint("InvalidChangeSet",
		": ",

		"ErrorMessage=",
		err.ErrorMessage,
	)
}

func (self LoadWithPersistErrorInvalidChangeSet) Is(target error) bool {
	return target == ErrLoadWithPersistErrorInvalidChangeSet
}

type LoadWithPersistErrorCouldNotLoad struct {
}

func NewLoadWithPersistErrorCouldNotLoad() *LoadWithPersistError {
	return &LoadWithPersistError{err: &LoadWithPersistErrorCouldNotLoad{}}
}

func (e LoadWithPersistErrorCouldNotLoad) destroy() {
}

func (err LoadWithPersistErrorCouldNotLoad) Error() string {
	return fmt.Sprint("CouldNotLoad")
}

func (self LoadWithPersistErrorCouldNotLoad) Is(target error) bool {
	return target == ErrLoadWithPersistErrorCouldNotLoad
}

type FfiConverterLoadWithPersistError struct{}

var FfiConverterLoadWithPersistErrorINSTANCE = FfiConverterLoadWithPersistError{}

func (c FfiConverterLoadWithPersistError) Lift(eb RustBufferI) *LoadWithPersistError {
	return LiftFromRustBuffer[*LoadWithPersistError](c, eb)
}

func (c FfiConverterLoadWithPersistError) Lower(value *LoadWithPersistError) C.RustBuffer {
	return LowerIntoRustBuffer[*LoadWithPersistError](c, value)
}

func (c FfiConverterLoadWithPersistError) Read(reader io.Reader) *LoadWithPersistError {
	errorID := readUint32(reader)

	switch errorID {
	case 1:
		return &LoadWithPersistError{&LoadWithPersistErrorPersist{
			ErrorMessage: FfiConverterStringINSTANCE.Read(reader),
		}}
	case 2:
		return &LoadWithPersistError{&LoadWithPersistErrorInvalidChangeSet{
			ErrorMessage: FfiConverterStringINSTANCE.Read(reader),
		}}
	case 3:
		return &LoadWithPersistError{&LoadWithPersistErrorCouldNotLoad{}}
	default:
		panic(fmt.Sprintf("Unknown error code %d in FfiConverterLoadWithPersistError.Read()", errorID))
	}
}

func (c FfiConverterLoadWithPersistError) Write(writer io.Writer, value *LoadWithPersistError) {
	switch variantValue := value.err.(type) {
	case *LoadWithPersistErrorPersist:
		writeInt32(writer, 1)
		FfiConverterStringINSTANCE.Write(writer, variantValue.ErrorMessage)
	case *LoadWithPersistErrorInvalidChangeSet:
		writeInt32(writer, 2)
		FfiConverterStringINSTANCE.Write(writer, variantValue.ErrorMessage)
	case *LoadWithPersistErrorCouldNotLoad:
		writeInt32(writer, 3)
	default:
		_ = variantValue
		panic(fmt.Sprintf("invalid error value `%v` in FfiConverterLoadWithPersistError.Write", value))
	}
}

type FfiDestroyerLoadWithPersistError struct{}

func (_ FfiDestroyerLoadWithPersistError) Destroy(value *LoadWithPersistError) {
	switch variantValue := value.err.(type) {
	case LoadWithPersistErrorPersist:
		variantValue.destroy()
	case LoadWithPersistErrorInvalidChangeSet:
		variantValue.destroy()
	case LoadWithPersistErrorCouldNotLoad:
		variantValue.destroy()
	default:
		_ = variantValue
		panic(fmt.Sprintf("invalid error value `%v` in FfiDestroyerLoadWithPersistError.Destroy", value))
	}
}

type LockTime interface {
	Destroy()
}
type LockTimeBlocks struct {
	Height uint32
}

func (e LockTimeBlocks) Destroy() {
	FfiDestroyerUint32{}.Destroy(e.Height)
}

type LockTimeSeconds struct {
	ConsensusTime uint32
}

func (e LockTimeSeconds) Destroy() {
	FfiDestroyerUint32{}.Destroy(e.ConsensusTime)
}

type FfiConverterLockTime struct{}

var FfiConverterLockTimeINSTANCE = FfiConverterLockTime{}

func (c FfiConverterLockTime) Lift(rb RustBufferI) LockTime {
	return LiftFromRustBuffer[LockTime](c, rb)
}

func (c FfiConverterLockTime) Lower(value LockTime) C.RustBuffer {
	return LowerIntoRustBuffer[LockTime](c, value)
}
func (FfiConverterLockTime) Read(reader io.Reader) LockTime {
	id := readInt32(reader)
	switch id {
	case 1:
		return LockTimeBlocks{
			FfiConverterUint32INSTANCE.Read(reader),
		}
	case 2:
		return LockTimeSeconds{
			FfiConverterUint32INSTANCE.Read(reader),
		}
	default:
		panic(fmt.Sprintf("invalid enum value %v in FfiConverterLockTime.Read()", id))
	}
}

func (FfiConverterLockTime) Write(writer io.Writer, value LockTime) {
	switch variant_value := value.(type) {
	case LockTimeBlocks:
		writeInt32(writer, 1)
		FfiConverterUint32INSTANCE.Write(writer, variant_value.Height)
	case LockTimeSeconds:
		writeInt32(writer, 2)
		FfiConverterUint32INSTANCE.Write(writer, variant_value.ConsensusTime)
	default:
		_ = variant_value
		panic(fmt.Sprintf("invalid enum value `%v` in FfiConverterLockTime.Write", value))
	}
}

type FfiDestroyerLockTime struct{}

func (_ FfiDestroyerLockTime) Destroy(value LockTime) {
	value.Destroy()
}

type MiniscriptError struct {
	err error
}

// Convience method to turn *MiniscriptError into error
// Avoiding treating nil pointer as non nil error interface
func (err *MiniscriptError) AsError() error {
	if err == nil {
		return nil
	} else {
		return err
	}
}

func (err MiniscriptError) Error() string {
	return fmt.Sprintf("MiniscriptError: %s", err.err.Error())
}

func (err MiniscriptError) Unwrap() error {
	return err.err
}

// Err* are used for checking error type with `errors.Is`
var ErrMiniscriptErrorAbsoluteLockTime = fmt.Errorf("MiniscriptErrorAbsoluteLockTime")
var ErrMiniscriptErrorAddrError = fmt.Errorf("MiniscriptErrorAddrError")
var ErrMiniscriptErrorAddrP2shError = fmt.Errorf("MiniscriptErrorAddrP2shError")
var ErrMiniscriptErrorAnalysisError = fmt.Errorf("MiniscriptErrorAnalysisError")
var ErrMiniscriptErrorAtOutsideOr = fmt.Errorf("MiniscriptErrorAtOutsideOr")
var ErrMiniscriptErrorBadDescriptor = fmt.Errorf("MiniscriptErrorBadDescriptor")
var ErrMiniscriptErrorBareDescriptorAddr = fmt.Errorf("MiniscriptErrorBareDescriptorAddr")
var ErrMiniscriptErrorCmsTooManyKeys = fmt.Errorf("MiniscriptErrorCmsTooManyKeys")
var ErrMiniscriptErrorContextError = fmt.Errorf("MiniscriptErrorContextError")
var ErrMiniscriptErrorCouldNotSatisfy = fmt.Errorf("MiniscriptErrorCouldNotSatisfy")
var ErrMiniscriptErrorExpectedChar = fmt.Errorf("MiniscriptErrorExpectedChar")
var ErrMiniscriptErrorImpossibleSatisfaction = fmt.Errorf("MiniscriptErrorImpossibleSatisfaction")
var ErrMiniscriptErrorInvalidOpcode = fmt.Errorf("MiniscriptErrorInvalidOpcode")
var ErrMiniscriptErrorInvalidPush = fmt.Errorf("MiniscriptErrorInvalidPush")
var ErrMiniscriptErrorLiftError = fmt.Errorf("MiniscriptErrorLiftError")
var ErrMiniscriptErrorMaxRecursiveDepthExceeded = fmt.Errorf("MiniscriptErrorMaxRecursiveDepthExceeded")
var ErrMiniscriptErrorMissingSig = fmt.Errorf("MiniscriptErrorMissingSig")
var ErrMiniscriptErrorMultiATooManyKeys = fmt.Errorf("MiniscriptErrorMultiATooManyKeys")
var ErrMiniscriptErrorMultiColon = fmt.Errorf("MiniscriptErrorMultiColon")
var ErrMiniscriptErrorMultipathDescLenMismatch = fmt.Errorf("MiniscriptErrorMultipathDescLenMismatch")
var ErrMiniscriptErrorNonMinimalVerify = fmt.Errorf("MiniscriptErrorNonMinimalVerify")
var ErrMiniscriptErrorNonStandardBareScript = fmt.Errorf("MiniscriptErrorNonStandardBareScript")
var ErrMiniscriptErrorNonTopLevel = fmt.Errorf("MiniscriptErrorNonTopLevel")
var ErrMiniscriptErrorParseThreshold = fmt.Errorf("MiniscriptErrorParseThreshold")
var ErrMiniscriptErrorPolicyError = fmt.Errorf("MiniscriptErrorPolicyError")
var ErrMiniscriptErrorPubKeyCtxError = fmt.Errorf("MiniscriptErrorPubKeyCtxError")
var ErrMiniscriptErrorRelativeLockTime = fmt.Errorf("MiniscriptErrorRelativeLockTime")
var ErrMiniscriptErrorScript = fmt.Errorf("MiniscriptErrorScript")
var ErrMiniscriptErrorSecp = fmt.Errorf("MiniscriptErrorSecp")
var ErrMiniscriptErrorThreshold = fmt.Errorf("MiniscriptErrorThreshold")
var ErrMiniscriptErrorTrNoScriptCode = fmt.Errorf("MiniscriptErrorTrNoScriptCode")
var ErrMiniscriptErrorTrailing = fmt.Errorf("MiniscriptErrorTrailing")
var ErrMiniscriptErrorTypeCheck = fmt.Errorf("MiniscriptErrorTypeCheck")
var ErrMiniscriptErrorUnexpected = fmt.Errorf("MiniscriptErrorUnexpected")
var ErrMiniscriptErrorUnexpectedStart = fmt.Errorf("MiniscriptErrorUnexpectedStart")
var ErrMiniscriptErrorUnknownWrapper = fmt.Errorf("MiniscriptErrorUnknownWrapper")
var ErrMiniscriptErrorUnprintable = fmt.Errorf("MiniscriptErrorUnprintable")

// Variant structs
type MiniscriptErrorAbsoluteLockTime struct {
}

func NewMiniscriptErrorAbsoluteLockTime() *MiniscriptError {
	return &MiniscriptError{err: &MiniscriptErrorAbsoluteLockTime{}}
}

func (e MiniscriptErrorAbsoluteLockTime) destroy() {
}

func (err MiniscriptErrorAbsoluteLockTime) Error() string {
	return fmt.Sprint("AbsoluteLockTime")
}

func (self MiniscriptErrorAbsoluteLockTime) Is(target error) bool {
	return target == ErrMiniscriptErrorAbsoluteLockTime
}

type MiniscriptErrorAddrError struct {
	ErrorMessage string
}

func NewMiniscriptErrorAddrError(
	errorMessage string,
) *MiniscriptError {
	return &MiniscriptError{err: &MiniscriptErrorAddrError{
		ErrorMessage: errorMessage}}
}

func (e MiniscriptErrorAddrError) destroy() {
	FfiDestroyerString{}.Destroy(e.ErrorMessage)
}

func (err MiniscriptErrorAddrError) Error() string {
	return fmt.Sprint("AddrError",
		": ",

		"ErrorMessage=",
		err.ErrorMessage,
	)
}

func (self MiniscriptErrorAddrError) Is(target error) bool {
	return target == ErrMiniscriptErrorAddrError
}

type MiniscriptErrorAddrP2shError struct {
	ErrorMessage string
}

func NewMiniscriptErrorAddrP2shError(
	errorMessage string,
) *MiniscriptError {
	return &MiniscriptError{err: &MiniscriptErrorAddrP2shError{
		ErrorMessage: errorMessage}}
}

func (e MiniscriptErrorAddrP2shError) destroy() {
	FfiDestroyerString{}.Destroy(e.ErrorMessage)
}

func (err MiniscriptErrorAddrP2shError) Error() string {
	return fmt.Sprint("AddrP2shError",
		": ",

		"ErrorMessage=",
		err.ErrorMessage,
	)
}

func (self MiniscriptErrorAddrP2shError) Is(target error) bool {
	return target == ErrMiniscriptErrorAddrP2shError
}

type MiniscriptErrorAnalysisError struct {
	ErrorMessage string
}

func NewMiniscriptErrorAnalysisError(
	errorMessage string,
) *MiniscriptError {
	return &MiniscriptError{err: &MiniscriptErrorAnalysisError{
		ErrorMessage: errorMessage}}
}

func (e MiniscriptErrorAnalysisError) destroy() {
	FfiDestroyerString{}.Destroy(e.ErrorMessage)
}

func (err MiniscriptErrorAnalysisError) Error() string {
	return fmt.Sprint("AnalysisError",
		": ",

		"ErrorMessage=",
		err.ErrorMessage,
	)
}

func (self MiniscriptErrorAnalysisError) Is(target error) bool {
	return target == ErrMiniscriptErrorAnalysisError
}

type MiniscriptErrorAtOutsideOr struct {
}

func NewMiniscriptErrorAtOutsideOr() *MiniscriptError {
	return &MiniscriptError{err: &MiniscriptErrorAtOutsideOr{}}
}

func (e MiniscriptErrorAtOutsideOr) destroy() {
}

func (err MiniscriptErrorAtOutsideOr) Error() string {
	return fmt.Sprint("AtOutsideOr")
}

func (self MiniscriptErrorAtOutsideOr) Is(target error) bool {
	return target == ErrMiniscriptErrorAtOutsideOr
}

type MiniscriptErrorBadDescriptor struct {
	ErrorMessage string
}

func NewMiniscriptErrorBadDescriptor(
	errorMessage string,
) *MiniscriptError {
	return &MiniscriptError{err: &MiniscriptErrorBadDescriptor{
		ErrorMessage: errorMessage}}
}

func (e MiniscriptErrorBadDescriptor) destroy() {
	FfiDestroyerString{}.Destroy(e.ErrorMessage)
}

func (err MiniscriptErrorBadDescriptor) Error() string {
	return fmt.Sprint("BadDescriptor",
		": ",

		"ErrorMessage=",
		err.ErrorMessage,
	)
}

func (self MiniscriptErrorBadDescriptor) Is(target error) bool {
	return target == ErrMiniscriptErrorBadDescriptor
}

type MiniscriptErrorBareDescriptorAddr struct {
}

func NewMiniscriptErrorBareDescriptorAddr() *MiniscriptError {
	return &MiniscriptError{err: &MiniscriptErrorBareDescriptorAddr{}}
}

func (e MiniscriptErrorBareDescriptorAddr) destroy() {
}

func (err MiniscriptErrorBareDescriptorAddr) Error() string {
	return fmt.Sprint("BareDescriptorAddr")
}

func (self MiniscriptErrorBareDescriptorAddr) Is(target error) bool {
	return target == ErrMiniscriptErrorBareDescriptorAddr
}

type MiniscriptErrorCmsTooManyKeys struct {
	Keys uint32
}

func NewMiniscriptErrorCmsTooManyKeys(
	keys uint32,
) *MiniscriptError {
	return &MiniscriptError{err: &MiniscriptErrorCmsTooManyKeys{
		Keys: keys}}
}

func (e MiniscriptErrorCmsTooManyKeys) destroy() {
	FfiDestroyerUint32{}.Destroy(e.Keys)
}

func (err MiniscriptErrorCmsTooManyKeys) Error() string {
	return fmt.Sprint("CmsTooManyKeys",
		": ",

		"Keys=",
		err.Keys,
	)
}

func (self MiniscriptErrorCmsTooManyKeys) Is(target error) bool {
	return target == ErrMiniscriptErrorCmsTooManyKeys
}

type MiniscriptErrorContextError struct {
	ErrorMessage string
}

func NewMiniscriptErrorContextError(
	errorMessage string,
) *MiniscriptError {
	return &MiniscriptError{err: &MiniscriptErrorContextError{
		ErrorMessage: errorMessage}}
}

func (e MiniscriptErrorContextError) destroy() {
	FfiDestroyerString{}.Destroy(e.ErrorMessage)
}

func (err MiniscriptErrorContextError) Error() string {
	return fmt.Sprint("ContextError",
		": ",

		"ErrorMessage=",
		err.ErrorMessage,
	)
}

func (self MiniscriptErrorContextError) Is(target error) bool {
	return target == ErrMiniscriptErrorContextError
}

type MiniscriptErrorCouldNotSatisfy struct {
}

func NewMiniscriptErrorCouldNotSatisfy() *MiniscriptError {
	return &MiniscriptError{err: &MiniscriptErrorCouldNotSatisfy{}}
}

func (e MiniscriptErrorCouldNotSatisfy) destroy() {
}

func (err MiniscriptErrorCouldNotSatisfy) Error() string {
	return fmt.Sprint("CouldNotSatisfy")
}

func (self MiniscriptErrorCouldNotSatisfy) Is(target error) bool {
	return target == ErrMiniscriptErrorCouldNotSatisfy
}

type MiniscriptErrorExpectedChar struct {
	Char string
}

func NewMiniscriptErrorExpectedChar(
	char string,
) *MiniscriptError {
	return &MiniscriptError{err: &MiniscriptErrorExpectedChar{
		Char: char}}
}

func (e MiniscriptErrorExpectedChar) destroy() {
	FfiDestroyerString{}.Destroy(e.Char)
}

func (err MiniscriptErrorExpectedChar) Error() string {
	return fmt.Sprint("ExpectedChar",
		": ",

		"Char=",
		err.Char,
	)
}

func (self MiniscriptErrorExpectedChar) Is(target error) bool {
	return target == ErrMiniscriptErrorExpectedChar
}

type MiniscriptErrorImpossibleSatisfaction struct {
}

func NewMiniscriptErrorImpossibleSatisfaction() *MiniscriptError {
	return &MiniscriptError{err: &MiniscriptErrorImpossibleSatisfaction{}}
}

func (e MiniscriptErrorImpossibleSatisfaction) destroy() {
}

func (err MiniscriptErrorImpossibleSatisfaction) Error() string {
	return fmt.Sprint("ImpossibleSatisfaction")
}

func (self MiniscriptErrorImpossibleSatisfaction) Is(target error) bool {
	return target == ErrMiniscriptErrorImpossibleSatisfaction
}

type MiniscriptErrorInvalidOpcode struct {
}

func NewMiniscriptErrorInvalidOpcode() *MiniscriptError {
	return &MiniscriptError{err: &MiniscriptErrorInvalidOpcode{}}
}

func (e MiniscriptErrorInvalidOpcode) destroy() {
}

func (err MiniscriptErrorInvalidOpcode) Error() string {
	return fmt.Sprint("InvalidOpcode")
}

func (self MiniscriptErrorInvalidOpcode) Is(target error) bool {
	return target == ErrMiniscriptErrorInvalidOpcode
}

type MiniscriptErrorInvalidPush struct {
}

func NewMiniscriptErrorInvalidPush() *MiniscriptError {
	return &MiniscriptError{err: &MiniscriptErrorInvalidPush{}}
}

func (e MiniscriptErrorInvalidPush) destroy() {
}

func (err MiniscriptErrorInvalidPush) Error() string {
	return fmt.Sprint("InvalidPush")
}

func (self MiniscriptErrorInvalidPush) Is(target error) bool {
	return target == ErrMiniscriptErrorInvalidPush
}

type MiniscriptErrorLiftError struct {
	ErrorMessage string
}

func NewMiniscriptErrorLiftError(
	errorMessage string,
) *MiniscriptError {
	return &MiniscriptError{err: &MiniscriptErrorLiftError{
		ErrorMessage: errorMessage}}
}

func (e MiniscriptErrorLiftError) destroy() {
	FfiDestroyerString{}.Destroy(e.ErrorMessage)
}

func (err MiniscriptErrorLiftError) Error() string {
	return fmt.Sprint("LiftError",
		": ",

		"ErrorMessage=",
		err.ErrorMessage,
	)
}

func (self MiniscriptErrorLiftError) Is(target error) bool {
	return target == ErrMiniscriptErrorLiftError
}

type MiniscriptErrorMaxRecursiveDepthExceeded struct {
}

func NewMiniscriptErrorMaxRecursiveDepthExceeded() *MiniscriptError {
	return &MiniscriptError{err: &MiniscriptErrorMaxRecursiveDepthExceeded{}}
}

func (e MiniscriptErrorMaxRecursiveDepthExceeded) destroy() {
}

func (err MiniscriptErrorMaxRecursiveDepthExceeded) Error() string {
	return fmt.Sprint("MaxRecursiveDepthExceeded")
}

func (self MiniscriptErrorMaxRecursiveDepthExceeded) Is(target error) bool {
	return target == ErrMiniscriptErrorMaxRecursiveDepthExceeded
}

type MiniscriptErrorMissingSig struct {
}

func NewMiniscriptErrorMissingSig() *MiniscriptError {
	return &MiniscriptError{err: &MiniscriptErrorMissingSig{}}
}

func (e MiniscriptErrorMissingSig) destroy() {
}

func (err MiniscriptErrorMissingSig) Error() string {
	return fmt.Sprint("MissingSig")
}

func (self MiniscriptErrorMissingSig) Is(target error) bool {
	return target == ErrMiniscriptErrorMissingSig
}

type MiniscriptErrorMultiATooManyKeys struct {
	Keys uint64
}

func NewMiniscriptErrorMultiATooManyKeys(
	keys uint64,
) *MiniscriptError {
	return &MiniscriptError{err: &MiniscriptErrorMultiATooManyKeys{
		Keys: keys}}
}

func (e MiniscriptErrorMultiATooManyKeys) destroy() {
	FfiDestroyerUint64{}.Destroy(e.Keys)
}

func (err MiniscriptErrorMultiATooManyKeys) Error() string {
	return fmt.Sprint("MultiATooManyKeys",
		": ",

		"Keys=",
		err.Keys,
	)
}

func (self MiniscriptErrorMultiATooManyKeys) Is(target error) bool {
	return target == ErrMiniscriptErrorMultiATooManyKeys
}

type MiniscriptErrorMultiColon struct {
}

func NewMiniscriptErrorMultiColon() *MiniscriptError {
	return &MiniscriptError{err: &MiniscriptErrorMultiColon{}}
}

func (e MiniscriptErrorMultiColon) destroy() {
}

func (err MiniscriptErrorMultiColon) Error() string {
	return fmt.Sprint("MultiColon")
}

func (self MiniscriptErrorMultiColon) Is(target error) bool {
	return target == ErrMiniscriptErrorMultiColon
}

type MiniscriptErrorMultipathDescLenMismatch struct {
}

func NewMiniscriptErrorMultipathDescLenMismatch() *MiniscriptError {
	return &MiniscriptError{err: &MiniscriptErrorMultipathDescLenMismatch{}}
}

func (e MiniscriptErrorMultipathDescLenMismatch) destroy() {
}

func (err MiniscriptErrorMultipathDescLenMismatch) Error() string {
	return fmt.Sprint("MultipathDescLenMismatch")
}

func (self MiniscriptErrorMultipathDescLenMismatch) Is(target error) bool {
	return target == ErrMiniscriptErrorMultipathDescLenMismatch
}

type MiniscriptErrorNonMinimalVerify struct {
	ErrorMessage string
}

func NewMiniscriptErrorNonMinimalVerify(
	errorMessage string,
) *MiniscriptError {
	return &MiniscriptError{err: &MiniscriptErrorNonMinimalVerify{
		ErrorMessage: errorMessage}}
}

func (e MiniscriptErrorNonMinimalVerify) destroy() {
	FfiDestroyerString{}.Destroy(e.ErrorMessage)
}

func (err MiniscriptErrorNonMinimalVerify) Error() string {
	return fmt.Sprint("NonMinimalVerify",
		": ",

		"ErrorMessage=",
		err.ErrorMessage,
	)
}

func (self MiniscriptErrorNonMinimalVerify) Is(target error) bool {
	return target == ErrMiniscriptErrorNonMinimalVerify
}

type MiniscriptErrorNonStandardBareScript struct {
}

func NewMiniscriptErrorNonStandardBareScript() *MiniscriptError {
	return &MiniscriptError{err: &MiniscriptErrorNonStandardBareScript{}}
}

func (e MiniscriptErrorNonStandardBareScript) destroy() {
}

func (err MiniscriptErrorNonStandardBareScript) Error() string {
	return fmt.Sprint("NonStandardBareScript")
}

func (self MiniscriptErrorNonStandardBareScript) Is(target error) bool {
	return target == ErrMiniscriptErrorNonStandardBareScript
}

type MiniscriptErrorNonTopLevel struct {
	ErrorMessage string
}

func NewMiniscriptErrorNonTopLevel(
	errorMessage string,
) *MiniscriptError {
	return &MiniscriptError{err: &MiniscriptErrorNonTopLevel{
		ErrorMessage: errorMessage}}
}

func (e MiniscriptErrorNonTopLevel) destroy() {
	FfiDestroyerString{}.Destroy(e.ErrorMessage)
}

func (err MiniscriptErrorNonTopLevel) Error() string {
	return fmt.Sprint("NonTopLevel",
		": ",

		"ErrorMessage=",
		err.ErrorMessage,
	)
}

func (self MiniscriptErrorNonTopLevel) Is(target error) bool {
	return target == ErrMiniscriptErrorNonTopLevel
}

type MiniscriptErrorParseThreshold struct {
}

func NewMiniscriptErrorParseThreshold() *MiniscriptError {
	return &MiniscriptError{err: &MiniscriptErrorParseThreshold{}}
}

func (e MiniscriptErrorParseThreshold) destroy() {
}

func (err MiniscriptErrorParseThreshold) Error() string {
	return fmt.Sprint("ParseThreshold")
}

func (self MiniscriptErrorParseThreshold) Is(target error) bool {
	return target == ErrMiniscriptErrorParseThreshold
}

type MiniscriptErrorPolicyError struct {
	ErrorMessage string
}

func NewMiniscriptErrorPolicyError(
	errorMessage string,
) *MiniscriptError {
	return &MiniscriptError{err: &MiniscriptErrorPolicyError{
		ErrorMessage: errorMessage}}
}

func (e MiniscriptErrorPolicyError) destroy() {
	FfiDestroyerString{}.Destroy(e.ErrorMessage)
}

func (err MiniscriptErrorPolicyError) Error() string {
	return fmt.Sprint("PolicyError",
		": ",

		"ErrorMessage=",
		err.ErrorMessage,
	)
}

func (self MiniscriptErrorPolicyError) Is(target error) bool {
	return target == ErrMiniscriptErrorPolicyError
}

type MiniscriptErrorPubKeyCtxError struct {
}

func NewMiniscriptErrorPubKeyCtxError() *MiniscriptError {
	return &MiniscriptError{err: &MiniscriptErrorPubKeyCtxError{}}
}

func (e MiniscriptErrorPubKeyCtxError) destroy() {
}

func (err MiniscriptErrorPubKeyCtxError) Error() string {
	return fmt.Sprint("PubKeyCtxError")
}

func (self MiniscriptErrorPubKeyCtxError) Is(target error) bool {
	return target == ErrMiniscriptErrorPubKeyCtxError
}

type MiniscriptErrorRelativeLockTime struct {
}

func NewMiniscriptErrorRelativeLockTime() *MiniscriptError {
	return &MiniscriptError{err: &MiniscriptErrorRelativeLockTime{}}
}

func (e MiniscriptErrorRelativeLockTime) destroy() {
}

func (err MiniscriptErrorRelativeLockTime) Error() string {
	return fmt.Sprint("RelativeLockTime")
}

func (self MiniscriptErrorRelativeLockTime) Is(target error) bool {
	return target == ErrMiniscriptErrorRelativeLockTime
}

type MiniscriptErrorScript struct {
	ErrorMessage string
}

func NewMiniscriptErrorScript(
	errorMessage string,
) *MiniscriptError {
	return &MiniscriptError{err: &MiniscriptErrorScript{
		ErrorMessage: errorMessage}}
}

func (e MiniscriptErrorScript) destroy() {
	FfiDestroyerString{}.Destroy(e.ErrorMessage)
}

func (err MiniscriptErrorScript) Error() string {
	return fmt.Sprint("Script",
		": ",

		"ErrorMessage=",
		err.ErrorMessage,
	)
}

func (self MiniscriptErrorScript) Is(target error) bool {
	return target == ErrMiniscriptErrorScript
}

type MiniscriptErrorSecp struct {
	ErrorMessage string
}

func NewMiniscriptErrorSecp(
	errorMessage string,
) *MiniscriptError {
	return &MiniscriptError{err: &MiniscriptErrorSecp{
		ErrorMessage: errorMessage}}
}

func (e MiniscriptErrorSecp) destroy() {
	FfiDestroyerString{}.Destroy(e.ErrorMessage)
}

func (err MiniscriptErrorSecp) Error() string {
	return fmt.Sprint("Secp",
		": ",

		"ErrorMessage=",
		err.ErrorMessage,
	)
}

func (self MiniscriptErrorSecp) Is(target error) bool {
	return target == ErrMiniscriptErrorSecp
}

type MiniscriptErrorThreshold struct {
}

func NewMiniscriptErrorThreshold() *MiniscriptError {
	return &MiniscriptError{err: &MiniscriptErrorThreshold{}}
}

func (e MiniscriptErrorThreshold) destroy() {
}

func (err MiniscriptErrorThreshold) Error() string {
	return fmt.Sprint("Threshold")
}

func (self MiniscriptErrorThreshold) Is(target error) bool {
	return target == ErrMiniscriptErrorThreshold
}

type MiniscriptErrorTrNoScriptCode struct {
}

func NewMiniscriptErrorTrNoScriptCode() *MiniscriptError {
	return &MiniscriptError{err: &MiniscriptErrorTrNoScriptCode{}}
}

func (e MiniscriptErrorTrNoScriptCode) destroy() {
}

func (err MiniscriptErrorTrNoScriptCode) Error() string {
	return fmt.Sprint("TrNoScriptCode")
}

func (self MiniscriptErrorTrNoScriptCode) Is(target error) bool {
	return target == ErrMiniscriptErrorTrNoScriptCode
}

type MiniscriptErrorTrailing struct {
	ErrorMessage string
}

func NewMiniscriptErrorTrailing(
	errorMessage string,
) *MiniscriptError {
	return &MiniscriptError{err: &MiniscriptErrorTrailing{
		ErrorMessage: errorMessage}}
}

func (e MiniscriptErrorTrailing) destroy() {
	FfiDestroyerString{}.Destroy(e.ErrorMessage)
}

func (err MiniscriptErrorTrailing) Error() string {
	return fmt.Sprint("Trailing",
		": ",

		"ErrorMessage=",
		err.ErrorMessage,
	)
}

func (self MiniscriptErrorTrailing) Is(target error) bool {
	return target == ErrMiniscriptErrorTrailing
}

type MiniscriptErrorTypeCheck struct {
	ErrorMessage string
}

func NewMiniscriptErrorTypeCheck(
	errorMessage string,
) *MiniscriptError {
	return &MiniscriptError{err: &MiniscriptErrorTypeCheck{
		ErrorMessage: errorMessage}}
}

func (e MiniscriptErrorTypeCheck) destroy() {
	FfiDestroyerString{}.Destroy(e.ErrorMessage)
}

func (err MiniscriptErrorTypeCheck) Error() string {
	return fmt.Sprint("TypeCheck",
		": ",

		"ErrorMessage=",
		err.ErrorMessage,
	)
}

func (self MiniscriptErrorTypeCheck) Is(target error) bool {
	return target == ErrMiniscriptErrorTypeCheck
}

type MiniscriptErrorUnexpected struct {
	ErrorMessage string
}

func NewMiniscriptErrorUnexpected(
	errorMessage string,
) *MiniscriptError {
	return &MiniscriptError{err: &MiniscriptErrorUnexpected{
		ErrorMessage: errorMessage}}
}

func (e MiniscriptErrorUnexpected) destroy() {
	FfiDestroyerString{}.Destroy(e.ErrorMessage)
}

func (err MiniscriptErrorUnexpected) Error() string {
	return fmt.Sprint("Unexpected",
		": ",

		"ErrorMessage=",
		err.ErrorMessage,
	)
}

func (self MiniscriptErrorUnexpected) Is(target error) bool {
	return target == ErrMiniscriptErrorUnexpected
}

type MiniscriptErrorUnexpectedStart struct {
}

func NewMiniscriptErrorUnexpectedStart() *MiniscriptError {
	return &MiniscriptError{err: &MiniscriptErrorUnexpectedStart{}}
}

func (e MiniscriptErrorUnexpectedStart) destroy() {
}

func (err MiniscriptErrorUnexpectedStart) Error() string {
	return fmt.Sprint("UnexpectedStart")
}

func (self MiniscriptErrorUnexpectedStart) Is(target error) bool {
	return target == ErrMiniscriptErrorUnexpectedStart
}

type MiniscriptErrorUnknownWrapper struct {
	Char string
}

func NewMiniscriptErrorUnknownWrapper(
	char string,
) *MiniscriptError {
	return &MiniscriptError{err: &MiniscriptErrorUnknownWrapper{
		Char: char}}
}

func (e MiniscriptErrorUnknownWrapper) destroy() {
	FfiDestroyerString{}.Destroy(e.Char)
}

func (err MiniscriptErrorUnknownWrapper) Error() string {
	return fmt.Sprint("UnknownWrapper",
		": ",

		"Char=",
		err.Char,
	)
}

func (self MiniscriptErrorUnknownWrapper) Is(target error) bool {
	return target == ErrMiniscriptErrorUnknownWrapper
}

type MiniscriptErrorUnprintable struct {
	Byte uint8
}

func NewMiniscriptErrorUnprintable(
	byte uint8,
) *MiniscriptError {
	return &MiniscriptError{err: &MiniscriptErrorUnprintable{
		Byte: byte}}
}

func (e MiniscriptErrorUnprintable) destroy() {
	FfiDestroyerUint8{}.Destroy(e.Byte)
}

func (err MiniscriptErrorUnprintable) Error() string {
	return fmt.Sprint("Unprintable",
		": ",

		"Byte=",
		err.Byte,
	)
}

func (self MiniscriptErrorUnprintable) Is(target error) bool {
	return target == ErrMiniscriptErrorUnprintable
}

type FfiConverterMiniscriptError struct{}

var FfiConverterMiniscriptErrorINSTANCE = FfiConverterMiniscriptError{}

func (c FfiConverterMiniscriptError) Lift(eb RustBufferI) *MiniscriptError {
	return LiftFromRustBuffer[*MiniscriptError](c, eb)
}

func (c FfiConverterMiniscriptError) Lower(value *MiniscriptError) C.RustBuffer {
	return LowerIntoRustBuffer[*MiniscriptError](c, value)
}

func (c FfiConverterMiniscriptError) Read(reader io.Reader) *MiniscriptError {
	errorID := readUint32(reader)

	switch errorID {
	case 1:
		return &MiniscriptError{&MiniscriptErrorAbsoluteLockTime{}}
	case 2:
		return &MiniscriptError{&MiniscriptErrorAddrError{
			ErrorMessage: FfiConverterStringINSTANCE.Read(reader),
		}}
	case 3:
		return &MiniscriptError{&MiniscriptErrorAddrP2shError{
			ErrorMessage: FfiConverterStringINSTANCE.Read(reader),
		}}
	case 4:
		return &MiniscriptError{&MiniscriptErrorAnalysisError{
			ErrorMessage: FfiConverterStringINSTANCE.Read(reader),
		}}
	case 5:
		return &MiniscriptError{&MiniscriptErrorAtOutsideOr{}}
	case 6:
		return &MiniscriptError{&MiniscriptErrorBadDescriptor{
			ErrorMessage: FfiConverterStringINSTANCE.Read(reader),
		}}
	case 7:
		return &MiniscriptError{&MiniscriptErrorBareDescriptorAddr{}}
	case 8:
		return &MiniscriptError{&MiniscriptErrorCmsTooManyKeys{
			Keys: FfiConverterUint32INSTANCE.Read(reader),
		}}
	case 9:
		return &MiniscriptError{&MiniscriptErrorContextError{
			ErrorMessage: FfiConverterStringINSTANCE.Read(reader),
		}}
	case 10:
		return &MiniscriptError{&MiniscriptErrorCouldNotSatisfy{}}
	case 11:
		return &MiniscriptError{&MiniscriptErrorExpectedChar{
			Char: FfiConverterStringINSTANCE.Read(reader),
		}}
	case 12:
		return &MiniscriptError{&MiniscriptErrorImpossibleSatisfaction{}}
	case 13:
		return &MiniscriptError{&MiniscriptErrorInvalidOpcode{}}
	case 14:
		return &MiniscriptError{&MiniscriptErrorInvalidPush{}}
	case 15:
		return &MiniscriptError{&MiniscriptErrorLiftError{
			ErrorMessage: FfiConverterStringINSTANCE.Read(reader),
		}}
	case 16:
		return &MiniscriptError{&MiniscriptErrorMaxRecursiveDepthExceeded{}}
	case 17:
		return &MiniscriptError{&MiniscriptErrorMissingSig{}}
	case 18:
		return &MiniscriptError{&MiniscriptErrorMultiATooManyKeys{
			Keys: FfiConverterUint64INSTANCE.Read(reader),
		}}
	case 19:
		return &MiniscriptError{&MiniscriptErrorMultiColon{}}
	case 20:
		return &MiniscriptError{&MiniscriptErrorMultipathDescLenMismatch{}}
	case 21:
		return &MiniscriptError{&MiniscriptErrorNonMinimalVerify{
			ErrorMessage: FfiConverterStringINSTANCE.Read(reader),
		}}
	case 22:
		return &MiniscriptError{&MiniscriptErrorNonStandardBareScript{}}
	case 23:
		return &MiniscriptError{&MiniscriptErrorNonTopLevel{
			ErrorMessage: FfiConverterStringINSTANCE.Read(reader),
		}}
	case 24:
		return &MiniscriptError{&MiniscriptErrorParseThreshold{}}
	case 25:
		return &MiniscriptError{&MiniscriptErrorPolicyError{
			ErrorMessage: FfiConverterStringINSTANCE.Read(reader),
		}}
	case 26:
		return &MiniscriptError{&MiniscriptErrorPubKeyCtxError{}}
	case 27:
		return &MiniscriptError{&MiniscriptErrorRelativeLockTime{}}
	case 28:
		return &MiniscriptError{&MiniscriptErrorScript{
			ErrorMessage: FfiConverterStringINSTANCE.Read(reader),
		}}
	case 29:
		return &MiniscriptError{&MiniscriptErrorSecp{
			ErrorMessage: FfiConverterStringINSTANCE.Read(reader),
		}}
	case 30:
		return &MiniscriptError{&MiniscriptErrorThreshold{}}
	case 31:
		return &MiniscriptError{&MiniscriptErrorTrNoScriptCode{}}
	case 32:
		return &MiniscriptError{&MiniscriptErrorTrailing{
			ErrorMessage: FfiConverterStringINSTANCE.Read(reader),
		}}
	case 33:
		return &MiniscriptError{&MiniscriptErrorTypeCheck{
			ErrorMessage: FfiConverterStringINSTANCE.Read(reader),
		}}
	case 34:
		return &MiniscriptError{&MiniscriptErrorUnexpected{
			ErrorMessage: FfiConverterStringINSTANCE.Read(reader),
		}}
	case 35:
		return &MiniscriptError{&MiniscriptErrorUnexpectedStart{}}
	case 36:
		return &MiniscriptError{&MiniscriptErrorUnknownWrapper{
			Char: FfiConverterStringINSTANCE.Read(reader),
		}}
	case 37:
		return &MiniscriptError{&MiniscriptErrorUnprintable{
			Byte: FfiConverterUint8INSTANCE.Read(reader),
		}}
	default:
		panic(fmt.Sprintf("Unknown error code %d in FfiConverterMiniscriptError.Read()", errorID))
	}
}

func (c FfiConverterMiniscriptError) Write(writer io.Writer, value *MiniscriptError) {
	switch variantValue := value.err.(type) {
	case *MiniscriptErrorAbsoluteLockTime:
		writeInt32(writer, 1)
	case *MiniscriptErrorAddrError:
		writeInt32(writer, 2)
		FfiConverterStringINSTANCE.Write(writer, variantValue.ErrorMessage)
	case *MiniscriptErrorAddrP2shError:
		writeInt32(writer, 3)
		FfiConverterStringINSTANCE.Write(writer, variantValue.ErrorMessage)
	case *MiniscriptErrorAnalysisError:
		writeInt32(writer, 4)
		FfiConverterStringINSTANCE.Write(writer, variantValue.ErrorMessage)
	case *MiniscriptErrorAtOutsideOr:
		writeInt32(writer, 5)
	case *MiniscriptErrorBadDescriptor:
		writeInt32(writer, 6)
		FfiConverterStringINSTANCE.Write(writer, variantValue.ErrorMessage)
	case *MiniscriptErrorBareDescriptorAddr:
		writeInt32(writer, 7)
	case *MiniscriptErrorCmsTooManyKeys:
		writeInt32(writer, 8)
		FfiConverterUint32INSTANCE.Write(writer, variantValue.Keys)
	case *MiniscriptErrorContextError:
		writeInt32(writer, 9)
		FfiConverterStringINSTANCE.Write(writer, variantValue.ErrorMessage)
	case *MiniscriptErrorCouldNotSatisfy:
		writeInt32(writer, 10)
	case *MiniscriptErrorExpectedChar:
		writeInt32(writer, 11)
		FfiConverterStringINSTANCE.Write(writer, variantValue.Char)
	case *MiniscriptErrorImpossibleSatisfaction:
		writeInt32(writer, 12)
	case *MiniscriptErrorInvalidOpcode:
		writeInt32(writer, 13)
	case *MiniscriptErrorInvalidPush:
		writeInt32(writer, 14)
	case *MiniscriptErrorLiftError:
		writeInt32(writer, 15)
		FfiConverterStringINSTANCE.Write(writer, variantValue.ErrorMessage)
	case *MiniscriptErrorMaxRecursiveDepthExceeded:
		writeInt32(writer, 16)
	case *MiniscriptErrorMissingSig:
		writeInt32(writer, 17)
	case *MiniscriptErrorMultiATooManyKeys:
		writeInt32(writer, 18)
		FfiConverterUint64INSTANCE.Write(writer, variantValue.Keys)
	case *MiniscriptErrorMultiColon:
		writeInt32(writer, 19)
	case *MiniscriptErrorMultipathDescLenMismatch:
		writeInt32(writer, 20)
	case *MiniscriptErrorNonMinimalVerify:
		writeInt32(writer, 21)
		FfiConverterStringINSTANCE.Write(writer, variantValue.ErrorMessage)
	case *MiniscriptErrorNonStandardBareScript:
		writeInt32(writer, 22)
	case *MiniscriptErrorNonTopLevel:
		writeInt32(writer, 23)
		FfiConverterStringINSTANCE.Write(writer, variantValue.ErrorMessage)
	case *MiniscriptErrorParseThreshold:
		writeInt32(writer, 24)
	case *MiniscriptErrorPolicyError:
		writeInt32(writer, 25)
		FfiConverterStringINSTANCE.Write(writer, variantValue.ErrorMessage)
	case *MiniscriptErrorPubKeyCtxError:
		writeInt32(writer, 26)
	case *MiniscriptErrorRelativeLockTime:
		writeInt32(writer, 27)
	case *MiniscriptErrorScript:
		writeInt32(writer, 28)
		FfiConverterStringINSTANCE.Write(writer, variantValue.ErrorMessage)
	case *MiniscriptErrorSecp:
		writeInt32(writer, 29)
		FfiConverterStringINSTANCE.Write(writer, variantValue.ErrorMessage)
	case *MiniscriptErrorThreshold:
		writeInt32(writer, 30)
	case *MiniscriptErrorTrNoScriptCode:
		writeInt32(writer, 31)
	case *MiniscriptErrorTrailing:
		writeInt32(writer, 32)
		FfiConverterStringINSTANCE.Write(writer, variantValue.ErrorMessage)
	case *MiniscriptErrorTypeCheck:
		writeInt32(writer, 33)
		FfiConverterStringINSTANCE.Write(writer, variantValue.ErrorMessage)
	case *MiniscriptErrorUnexpected:
		writeInt32(writer, 34)
		FfiConverterStringINSTANCE.Write(writer, variantValue.ErrorMessage)
	case *MiniscriptErrorUnexpectedStart:
		writeInt32(writer, 35)
	case *MiniscriptErrorUnknownWrapper:
		writeInt32(writer, 36)
		FfiConverterStringINSTANCE.Write(writer, variantValue.Char)
	case *MiniscriptErrorUnprintable:
		writeInt32(writer, 37)
		FfiConverterUint8INSTANCE.Write(writer, variantValue.Byte)
	default:
		_ = variantValue
		panic(fmt.Sprintf("invalid error value `%v` in FfiConverterMiniscriptError.Write", value))
	}
}

type FfiDestroyerMiniscriptError struct{}

func (_ FfiDestroyerMiniscriptError) Destroy(value *MiniscriptError) {
	switch variantValue := value.err.(type) {
	case MiniscriptErrorAbsoluteLockTime:
		variantValue.destroy()
	case MiniscriptErrorAddrError:
		variantValue.destroy()
	case MiniscriptErrorAddrP2shError:
		variantValue.destroy()
	case MiniscriptErrorAnalysisError:
		variantValue.destroy()
	case MiniscriptErrorAtOutsideOr:
		variantValue.destroy()
	case MiniscriptErrorBadDescriptor:
		variantValue.destroy()
	case MiniscriptErrorBareDescriptorAddr:
		variantValue.destroy()
	case MiniscriptErrorCmsTooManyKeys:
		variantValue.destroy()
	case MiniscriptErrorContextError:
		variantValue.destroy()
	case MiniscriptErrorCouldNotSatisfy:
		variantValue.destroy()
	case MiniscriptErrorExpectedChar:
		variantValue.destroy()
	case MiniscriptErrorImpossibleSatisfaction:
		variantValue.destroy()
	case MiniscriptErrorInvalidOpcode:
		variantValue.destroy()
	case MiniscriptErrorInvalidPush:
		variantValue.destroy()
	case MiniscriptErrorLiftError:
		variantValue.destroy()
	case MiniscriptErrorMaxRecursiveDepthExceeded:
		variantValue.destroy()
	case MiniscriptErrorMissingSig:
		variantValue.destroy()
	case MiniscriptErrorMultiATooManyKeys:
		variantValue.destroy()
	case MiniscriptErrorMultiColon:
		variantValue.destroy()
	case MiniscriptErrorMultipathDescLenMismatch:
		variantValue.destroy()
	case MiniscriptErrorNonMinimalVerify:
		variantValue.destroy()
	case MiniscriptErrorNonStandardBareScript:
		variantValue.destroy()
	case MiniscriptErrorNonTopLevel:
		variantValue.destroy()
	case MiniscriptErrorParseThreshold:
		variantValue.destroy()
	case MiniscriptErrorPolicyError:
		variantValue.destroy()
	case MiniscriptErrorPubKeyCtxError:
		variantValue.destroy()
	case MiniscriptErrorRelativeLockTime:
		variantValue.destroy()
	case MiniscriptErrorScript:
		variantValue.destroy()
	case MiniscriptErrorSecp:
		variantValue.destroy()
	case MiniscriptErrorThreshold:
		variantValue.destroy()
	case MiniscriptErrorTrNoScriptCode:
		variantValue.destroy()
	case MiniscriptErrorTrailing:
		variantValue.destroy()
	case MiniscriptErrorTypeCheck:
		variantValue.destroy()
	case MiniscriptErrorUnexpected:
		variantValue.destroy()
	case MiniscriptErrorUnexpectedStart:
		variantValue.destroy()
	case MiniscriptErrorUnknownWrapper:
		variantValue.destroy()
	case MiniscriptErrorUnprintable:
		variantValue.destroy()
	default:
		_ = variantValue
		panic(fmt.Sprintf("invalid error value `%v` in FfiDestroyerMiniscriptError.Destroy", value))
	}
}

type Network uint

const (
	NetworkBitcoin  Network = 1
	NetworkTestnet  Network = 2
	NetworkSignet   Network = 3
	NetworkRegtest  Network = 4
	NetworkTestnet4 Network = 5
)

type FfiConverterNetwork struct{}

var FfiConverterNetworkINSTANCE = FfiConverterNetwork{}

func (c FfiConverterNetwork) Lift(rb RustBufferI) Network {
	return LiftFromRustBuffer[Network](c, rb)
}

func (c FfiConverterNetwork) Lower(value Network) C.RustBuffer {
	return LowerIntoRustBuffer[Network](c, value)
}
func (FfiConverterNetwork) Read(reader io.Reader) Network {
	id := readInt32(reader)
	return Network(id)
}

func (FfiConverterNetwork) Write(writer io.Writer, value Network) {
	writeInt32(writer, int32(value))
}

type FfiDestroyerNetwork struct{}

func (_ FfiDestroyerNetwork) Destroy(value Network) {
}

type ParseAmountError struct {
	err error
}

// Convience method to turn *ParseAmountError into error
// Avoiding treating nil pointer as non nil error interface
func (err *ParseAmountError) AsError() error {
	if err == nil {
		return nil
	} else {
		return err
	}
}

func (err ParseAmountError) Error() string {
	return fmt.Sprintf("ParseAmountError: %s", err.err.Error())
}

func (err ParseAmountError) Unwrap() error {
	return err.err
}

// Err* are used for checking error type with `errors.Is`
var ErrParseAmountErrorOutOfRange = fmt.Errorf("ParseAmountErrorOutOfRange")
var ErrParseAmountErrorTooPrecise = fmt.Errorf("ParseAmountErrorTooPrecise")
var ErrParseAmountErrorMissingDigits = fmt.Errorf("ParseAmountErrorMissingDigits")
var ErrParseAmountErrorInputTooLarge = fmt.Errorf("ParseAmountErrorInputTooLarge")
var ErrParseAmountErrorInvalidCharacter = fmt.Errorf("ParseAmountErrorInvalidCharacter")
var ErrParseAmountErrorOtherParseAmountErr = fmt.Errorf("ParseAmountErrorOtherParseAmountErr")

// Variant structs
type ParseAmountErrorOutOfRange struct {
}

func NewParseAmountErrorOutOfRange() *ParseAmountError {
	return &ParseAmountError{err: &ParseAmountErrorOutOfRange{}}
}

func (e ParseAmountErrorOutOfRange) destroy() {
}

func (err ParseAmountErrorOutOfRange) Error() string {
	return fmt.Sprint("OutOfRange")
}

func (self ParseAmountErrorOutOfRange) Is(target error) bool {
	return target == ErrParseAmountErrorOutOfRange
}

type ParseAmountErrorTooPrecise struct {
}

func NewParseAmountErrorTooPrecise() *ParseAmountError {
	return &ParseAmountError{err: &ParseAmountErrorTooPrecise{}}
}

func (e ParseAmountErrorTooPrecise) destroy() {
}

func (err ParseAmountErrorTooPrecise) Error() string {
	return fmt.Sprint("TooPrecise")
}

func (self ParseAmountErrorTooPrecise) Is(target error) bool {
	return target == ErrParseAmountErrorTooPrecise
}

type ParseAmountErrorMissingDigits struct {
}

func NewParseAmountErrorMissingDigits() *ParseAmountError {
	return &ParseAmountError{err: &ParseAmountErrorMissingDigits{}}
}

func (e ParseAmountErrorMissingDigits) destroy() {
}

func (err ParseAmountErrorMissingDigits) Error() string {
	return fmt.Sprint("MissingDigits")
}

func (self ParseAmountErrorMissingDigits) Is(target error) bool {
	return target == ErrParseAmountErrorMissingDigits
}

type ParseAmountErrorInputTooLarge struct {
}

func NewParseAmountErrorInputTooLarge() *ParseAmountError {
	return &ParseAmountError{err: &ParseAmountErrorInputTooLarge{}}
}

func (e ParseAmountErrorInputTooLarge) destroy() {
}

func (err ParseAmountErrorInputTooLarge) Error() string {
	return fmt.Sprint("InputTooLarge")
}

func (self ParseAmountErrorInputTooLarge) Is(target error) bool {
	return target == ErrParseAmountErrorInputTooLarge
}

type ParseAmountErrorInvalidCharacter struct {
	ErrorMessage string
}

func NewParseAmountErrorInvalidCharacter(
	errorMessage string,
) *ParseAmountError {
	return &ParseAmountError{err: &ParseAmountErrorInvalidCharacter{
		ErrorMessage: errorMessage}}
}

func (e ParseAmountErrorInvalidCharacter) destroy() {
	FfiDestroyerString{}.Destroy(e.ErrorMessage)
}

func (err ParseAmountErrorInvalidCharacter) Error() string {
	return fmt.Sprint("InvalidCharacter",
		": ",

		"ErrorMessage=",
		err.ErrorMessage,
	)
}

func (self ParseAmountErrorInvalidCharacter) Is(target error) bool {
	return target == ErrParseAmountErrorInvalidCharacter
}

type ParseAmountErrorOtherParseAmountErr struct {
}

func NewParseAmountErrorOtherParseAmountErr() *ParseAmountError {
	return &ParseAmountError{err: &ParseAmountErrorOtherParseAmountErr{}}
}

func (e ParseAmountErrorOtherParseAmountErr) destroy() {
}

func (err ParseAmountErrorOtherParseAmountErr) Error() string {
	return fmt.Sprint("OtherParseAmountErr")
}

func (self ParseAmountErrorOtherParseAmountErr) Is(target error) bool {
	return target == ErrParseAmountErrorOtherParseAmountErr
}

type FfiConverterParseAmountError struct{}

var FfiConverterParseAmountErrorINSTANCE = FfiConverterParseAmountError{}

func (c FfiConverterParseAmountError) Lift(eb RustBufferI) *ParseAmountError {
	return LiftFromRustBuffer[*ParseAmountError](c, eb)
}

func (c FfiConverterParseAmountError) Lower(value *ParseAmountError) C.RustBuffer {
	return LowerIntoRustBuffer[*ParseAmountError](c, value)
}

func (c FfiConverterParseAmountError) Read(reader io.Reader) *ParseAmountError {
	errorID := readUint32(reader)

	switch errorID {
	case 1:
		return &ParseAmountError{&ParseAmountErrorOutOfRange{}}
	case 2:
		return &ParseAmountError{&ParseAmountErrorTooPrecise{}}
	case 3:
		return &ParseAmountError{&ParseAmountErrorMissingDigits{}}
	case 4:
		return &ParseAmountError{&ParseAmountErrorInputTooLarge{}}
	case 5:
		return &ParseAmountError{&ParseAmountErrorInvalidCharacter{
			ErrorMessage: FfiConverterStringINSTANCE.Read(reader),
		}}
	case 6:
		return &ParseAmountError{&ParseAmountErrorOtherParseAmountErr{}}
	default:
		panic(fmt.Sprintf("Unknown error code %d in FfiConverterParseAmountError.Read()", errorID))
	}
}

func (c FfiConverterParseAmountError) Write(writer io.Writer, value *ParseAmountError) {
	switch variantValue := value.err.(type) {
	case *ParseAmountErrorOutOfRange:
		writeInt32(writer, 1)
	case *ParseAmountErrorTooPrecise:
		writeInt32(writer, 2)
	case *ParseAmountErrorMissingDigits:
		writeInt32(writer, 3)
	case *ParseAmountErrorInputTooLarge:
		writeInt32(writer, 4)
	case *ParseAmountErrorInvalidCharacter:
		writeInt32(writer, 5)
		FfiConverterStringINSTANCE.Write(writer, variantValue.ErrorMessage)
	case *ParseAmountErrorOtherParseAmountErr:
		writeInt32(writer, 6)
	default:
		_ = variantValue
		panic(fmt.Sprintf("invalid error value `%v` in FfiConverterParseAmountError.Write", value))
	}
}

type FfiDestroyerParseAmountError struct{}

func (_ FfiDestroyerParseAmountError) Destroy(value *ParseAmountError) {
	switch variantValue := value.err.(type) {
	case ParseAmountErrorOutOfRange:
		variantValue.destroy()
	case ParseAmountErrorTooPrecise:
		variantValue.destroy()
	case ParseAmountErrorMissingDigits:
		variantValue.destroy()
	case ParseAmountErrorInputTooLarge:
		variantValue.destroy()
	case ParseAmountErrorInvalidCharacter:
		variantValue.destroy()
	case ParseAmountErrorOtherParseAmountErr:
		variantValue.destroy()
	default:
		_ = variantValue
		panic(fmt.Sprintf("invalid error value `%v` in FfiDestroyerParseAmountError.Destroy", value))
	}
}

type PersistenceError struct {
	err error
}

// Convience method to turn *PersistenceError into error
// Avoiding treating nil pointer as non nil error interface
func (err *PersistenceError) AsError() error {
	if err == nil {
		return nil
	} else {
		return err
	}
}

func (err PersistenceError) Error() string {
	return fmt.Sprintf("PersistenceError: %s", err.err.Error())
}

func (err PersistenceError) Unwrap() error {
	return err.err
}

// Err* are used for checking error type with `errors.Is`
var ErrPersistenceErrorWrite = fmt.Errorf("PersistenceErrorWrite")

// Variant structs
type PersistenceErrorWrite struct {
	ErrorMessage string
}

func NewPersistenceErrorWrite(
	errorMessage string,
) *PersistenceError {
	return &PersistenceError{err: &PersistenceErrorWrite{
		ErrorMessage: errorMessage}}
}

func (e PersistenceErrorWrite) destroy() {
	FfiDestroyerString{}.Destroy(e.ErrorMessage)
}

func (err PersistenceErrorWrite) Error() string {
	return fmt.Sprint("Write",
		": ",

		"ErrorMessage=",
		err.ErrorMessage,
	)
}

func (self PersistenceErrorWrite) Is(target error) bool {
	return target == ErrPersistenceErrorWrite
}

type FfiConverterPersistenceError struct{}

var FfiConverterPersistenceErrorINSTANCE = FfiConverterPersistenceError{}

func (c FfiConverterPersistenceError) Lift(eb RustBufferI) *PersistenceError {
	return LiftFromRustBuffer[*PersistenceError](c, eb)
}

func (c FfiConverterPersistenceError) Lower(value *PersistenceError) C.RustBuffer {
	return LowerIntoRustBuffer[*PersistenceError](c, value)
}

func (c FfiConverterPersistenceError) Read(reader io.Reader) *PersistenceError {
	errorID := readUint32(reader)

	switch errorID {
	case 1:
		return &PersistenceError{&PersistenceErrorWrite{
			ErrorMessage: FfiConverterStringINSTANCE.Read(reader),
		}}
	default:
		panic(fmt.Sprintf("Unknown error code %d in FfiConverterPersistenceError.Read()", errorID))
	}
}

func (c FfiConverterPersistenceError) Write(writer io.Writer, value *PersistenceError) {
	switch variantValue := value.err.(type) {
	case *PersistenceErrorWrite:
		writeInt32(writer, 1)
		FfiConverterStringINSTANCE.Write(writer, variantValue.ErrorMessage)
	default:
		_ = variantValue
		panic(fmt.Sprintf("invalid error value `%v` in FfiConverterPersistenceError.Write", value))
	}
}

type FfiDestroyerPersistenceError struct{}

func (_ FfiDestroyerPersistenceError) Destroy(value *PersistenceError) {
	switch variantValue := value.err.(type) {
	case PersistenceErrorWrite:
		variantValue.destroy()
	default:
		_ = variantValue
		panic(fmt.Sprintf("invalid error value `%v` in FfiDestroyerPersistenceError.Destroy", value))
	}
}

type PkOrF interface {
	Destroy()
}
type PkOrFPubkey struct {
	Value string
}

func (e PkOrFPubkey) Destroy() {
	FfiDestroyerString{}.Destroy(e.Value)
}

type PkOrFXOnlyPubkey struct {
	Value string
}

func (e PkOrFXOnlyPubkey) Destroy() {
	FfiDestroyerString{}.Destroy(e.Value)
}

type PkOrFFingerprint struct {
	Value string
}

func (e PkOrFFingerprint) Destroy() {
	FfiDestroyerString{}.Destroy(e.Value)
}

type FfiConverterPkOrF struct{}

var FfiConverterPkOrFINSTANCE = FfiConverterPkOrF{}

func (c FfiConverterPkOrF) Lift(rb RustBufferI) PkOrF {
	return LiftFromRustBuffer[PkOrF](c, rb)
}

func (c FfiConverterPkOrF) Lower(value PkOrF) C.RustBuffer {
	return LowerIntoRustBuffer[PkOrF](c, value)
}
func (FfiConverterPkOrF) Read(reader io.Reader) PkOrF {
	id := readInt32(reader)
	switch id {
	case 1:
		return PkOrFPubkey{
			FfiConverterStringINSTANCE.Read(reader),
		}
	case 2:
		return PkOrFXOnlyPubkey{
			FfiConverterStringINSTANCE.Read(reader),
		}
	case 3:
		return PkOrFFingerprint{
			FfiConverterStringINSTANCE.Read(reader),
		}
	default:
		panic(fmt.Sprintf("invalid enum value %v in FfiConverterPkOrF.Read()", id))
	}
}

func (FfiConverterPkOrF) Write(writer io.Writer, value PkOrF) {
	switch variant_value := value.(type) {
	case PkOrFPubkey:
		writeInt32(writer, 1)
		FfiConverterStringINSTANCE.Write(writer, variant_value.Value)
	case PkOrFXOnlyPubkey:
		writeInt32(writer, 2)
		FfiConverterStringINSTANCE.Write(writer, variant_value.Value)
	case PkOrFFingerprint:
		writeInt32(writer, 3)
		FfiConverterStringINSTANCE.Write(writer, variant_value.Value)
	default:
		_ = variant_value
		panic(fmt.Sprintf("invalid enum value `%v` in FfiConverterPkOrF.Write", value))
	}
}

type FfiDestroyerPkOrF struct{}

func (_ FfiDestroyerPkOrF) Destroy(value PkOrF) {
	value.Destroy()
}

type PsbtError struct {
	err error
}

// Convience method to turn *PsbtError into error
// Avoiding treating nil pointer as non nil error interface
func (err *PsbtError) AsError() error {
	if err == nil {
		return nil
	} else {
		return err
	}
}

func (err PsbtError) Error() string {
	return fmt.Sprintf("PsbtError: %s", err.err.Error())
}

func (err PsbtError) Unwrap() error {
	return err.err
}

// Err* are used for checking error type with `errors.Is`
var ErrPsbtErrorInvalidMagic = fmt.Errorf("PsbtErrorInvalidMagic")
var ErrPsbtErrorMissingUtxo = fmt.Errorf("PsbtErrorMissingUtxo")
var ErrPsbtErrorInvalidSeparator = fmt.Errorf("PsbtErrorInvalidSeparator")
var ErrPsbtErrorPsbtUtxoOutOfBounds = fmt.Errorf("PsbtErrorPsbtUtxoOutOfBounds")
var ErrPsbtErrorInvalidKey = fmt.Errorf("PsbtErrorInvalidKey")
var ErrPsbtErrorInvalidProprietaryKey = fmt.Errorf("PsbtErrorInvalidProprietaryKey")
var ErrPsbtErrorDuplicateKey = fmt.Errorf("PsbtErrorDuplicateKey")
var ErrPsbtErrorUnsignedTxHasScriptSigs = fmt.Errorf("PsbtErrorUnsignedTxHasScriptSigs")
var ErrPsbtErrorUnsignedTxHasScriptWitnesses = fmt.Errorf("PsbtErrorUnsignedTxHasScriptWitnesses")
var ErrPsbtErrorMustHaveUnsignedTx = fmt.Errorf("PsbtErrorMustHaveUnsignedTx")
var ErrPsbtErrorNoMorePairs = fmt.Errorf("PsbtErrorNoMorePairs")
var ErrPsbtErrorUnexpectedUnsignedTx = fmt.Errorf("PsbtErrorUnexpectedUnsignedTx")
var ErrPsbtErrorNonStandardSighashType = fmt.Errorf("PsbtErrorNonStandardSighashType")
var ErrPsbtErrorInvalidHash = fmt.Errorf("PsbtErrorInvalidHash")
var ErrPsbtErrorInvalidPreimageHashPair = fmt.Errorf("PsbtErrorInvalidPreimageHashPair")
var ErrPsbtErrorCombineInconsistentKeySources = fmt.Errorf("PsbtErrorCombineInconsistentKeySources")
var ErrPsbtErrorConsensusEncoding = fmt.Errorf("PsbtErrorConsensusEncoding")
var ErrPsbtErrorNegativeFee = fmt.Errorf("PsbtErrorNegativeFee")
var ErrPsbtErrorFeeOverflow = fmt.Errorf("PsbtErrorFeeOverflow")
var ErrPsbtErrorInvalidPublicKey = fmt.Errorf("PsbtErrorInvalidPublicKey")
var ErrPsbtErrorInvalidSecp256k1PublicKey = fmt.Errorf("PsbtErrorInvalidSecp256k1PublicKey")
var ErrPsbtErrorInvalidXOnlyPublicKey = fmt.Errorf("PsbtErrorInvalidXOnlyPublicKey")
var ErrPsbtErrorInvalidEcdsaSignature = fmt.Errorf("PsbtErrorInvalidEcdsaSignature")
var ErrPsbtErrorInvalidTaprootSignature = fmt.Errorf("PsbtErrorInvalidTaprootSignature")
var ErrPsbtErrorInvalidControlBlock = fmt.Errorf("PsbtErrorInvalidControlBlock")
var ErrPsbtErrorInvalidLeafVersion = fmt.Errorf("PsbtErrorInvalidLeafVersion")
var ErrPsbtErrorTaproot = fmt.Errorf("PsbtErrorTaproot")
var ErrPsbtErrorTapTree = fmt.Errorf("PsbtErrorTapTree")
var ErrPsbtErrorXPubKey = fmt.Errorf("PsbtErrorXPubKey")
var ErrPsbtErrorVersion = fmt.Errorf("PsbtErrorVersion")
var ErrPsbtErrorPartialDataConsumption = fmt.Errorf("PsbtErrorPartialDataConsumption")
var ErrPsbtErrorIo = fmt.Errorf("PsbtErrorIo")
var ErrPsbtErrorOtherPsbtErr = fmt.Errorf("PsbtErrorOtherPsbtErr")

// Variant structs
type PsbtErrorInvalidMagic struct {
}

func NewPsbtErrorInvalidMagic() *PsbtError {
	return &PsbtError{err: &PsbtErrorInvalidMagic{}}
}

func (e PsbtErrorInvalidMagic) destroy() {
}

func (err PsbtErrorInvalidMagic) Error() string {
	return fmt.Sprint("InvalidMagic")
}

func (self PsbtErrorInvalidMagic) Is(target error) bool {
	return target == ErrPsbtErrorInvalidMagic
}

type PsbtErrorMissingUtxo struct {
}

func NewPsbtErrorMissingUtxo() *PsbtError {
	return &PsbtError{err: &PsbtErrorMissingUtxo{}}
}

func (e PsbtErrorMissingUtxo) destroy() {
}

func (err PsbtErrorMissingUtxo) Error() string {
	return fmt.Sprint("MissingUtxo")
}

func (self PsbtErrorMissingUtxo) Is(target error) bool {
	return target == ErrPsbtErrorMissingUtxo
}

type PsbtErrorInvalidSeparator struct {
}

func NewPsbtErrorInvalidSeparator() *PsbtError {
	return &PsbtError{err: &PsbtErrorInvalidSeparator{}}
}

func (e PsbtErrorInvalidSeparator) destroy() {
}

func (err PsbtErrorInvalidSeparator) Error() string {
	return fmt.Sprint("InvalidSeparator")
}

func (self PsbtErrorInvalidSeparator) Is(target error) bool {
	return target == ErrPsbtErrorInvalidSeparator
}

type PsbtErrorPsbtUtxoOutOfBounds struct {
}

func NewPsbtErrorPsbtUtxoOutOfBounds() *PsbtError {
	return &PsbtError{err: &PsbtErrorPsbtUtxoOutOfBounds{}}
}

func (e PsbtErrorPsbtUtxoOutOfBounds) destroy() {
}

func (err PsbtErrorPsbtUtxoOutOfBounds) Error() string {
	return fmt.Sprint("PsbtUtxoOutOfBounds")
}

func (self PsbtErrorPsbtUtxoOutOfBounds) Is(target error) bool {
	return target == ErrPsbtErrorPsbtUtxoOutOfBounds
}

type PsbtErrorInvalidKey struct {
	Key string
}

func NewPsbtErrorInvalidKey(
	key string,
) *PsbtError {
	return &PsbtError{err: &PsbtErrorInvalidKey{
		Key: key}}
}

func (e PsbtErrorInvalidKey) destroy() {
	FfiDestroyerString{}.Destroy(e.Key)
}

func (err PsbtErrorInvalidKey) Error() string {
	return fmt.Sprint("InvalidKey",
		": ",

		"Key=",
		err.Key,
	)
}

func (self PsbtErrorInvalidKey) Is(target error) bool {
	return target == ErrPsbtErrorInvalidKey
}

type PsbtErrorInvalidProprietaryKey struct {
}

func NewPsbtErrorInvalidProprietaryKey() *PsbtError {
	return &PsbtError{err: &PsbtErrorInvalidProprietaryKey{}}
}

func (e PsbtErrorInvalidProprietaryKey) destroy() {
}

func (err PsbtErrorInvalidProprietaryKey) Error() string {
	return fmt.Sprint("InvalidProprietaryKey")
}

func (self PsbtErrorInvalidProprietaryKey) Is(target error) bool {
	return target == ErrPsbtErrorInvalidProprietaryKey
}

type PsbtErrorDuplicateKey struct {
	Key string
}

func NewPsbtErrorDuplicateKey(
	key string,
) *PsbtError {
	return &PsbtError{err: &PsbtErrorDuplicateKey{
		Key: key}}
}

func (e PsbtErrorDuplicateKey) destroy() {
	FfiDestroyerString{}.Destroy(e.Key)
}

func (err PsbtErrorDuplicateKey) Error() string {
	return fmt.Sprint("DuplicateKey",
		": ",

		"Key=",
		err.Key,
	)
}

func (self PsbtErrorDuplicateKey) Is(target error) bool {
	return target == ErrPsbtErrorDuplicateKey
}

type PsbtErrorUnsignedTxHasScriptSigs struct {
}

func NewPsbtErrorUnsignedTxHasScriptSigs() *PsbtError {
	return &PsbtError{err: &PsbtErrorUnsignedTxHasScriptSigs{}}
}

func (e PsbtErrorUnsignedTxHasScriptSigs) destroy() {
}

func (err PsbtErrorUnsignedTxHasScriptSigs) Error() string {
	return fmt.Sprint("UnsignedTxHasScriptSigs")
}

func (self PsbtErrorUnsignedTxHasScriptSigs) Is(target error) bool {
	return target == ErrPsbtErrorUnsignedTxHasScriptSigs
}

type PsbtErrorUnsignedTxHasScriptWitnesses struct {
}

func NewPsbtErrorUnsignedTxHasScriptWitnesses() *PsbtError {
	return &PsbtError{err: &PsbtErrorUnsignedTxHasScriptWitnesses{}}
}

func (e PsbtErrorUnsignedTxHasScriptWitnesses) destroy() {
}

func (err PsbtErrorUnsignedTxHasScriptWitnesses) Error() string {
	return fmt.Sprint("UnsignedTxHasScriptWitnesses")
}

func (self PsbtErrorUnsignedTxHasScriptWitnesses) Is(target error) bool {
	return target == ErrPsbtErrorUnsignedTxHasScriptWitnesses
}

type PsbtErrorMustHaveUnsignedTx struct {
}

func NewPsbtErrorMustHaveUnsignedTx() *PsbtError {
	return &PsbtError{err: &PsbtErrorMustHaveUnsignedTx{}}
}

func (e PsbtErrorMustHaveUnsignedTx) destroy() {
}

func (err PsbtErrorMustHaveUnsignedTx) Error() string {
	return fmt.Sprint("MustHaveUnsignedTx")
}

func (self PsbtErrorMustHaveUnsignedTx) Is(target error) bool {
	return target == ErrPsbtErrorMustHaveUnsignedTx
}

type PsbtErrorNoMorePairs struct {
}

func NewPsbtErrorNoMorePairs() *PsbtError {
	return &PsbtError{err: &PsbtErrorNoMorePairs{}}
}

func (e PsbtErrorNoMorePairs) destroy() {
}

func (err PsbtErrorNoMorePairs) Error() string {
	return fmt.Sprint("NoMorePairs")
}

func (self PsbtErrorNoMorePairs) Is(target error) bool {
	return target == ErrPsbtErrorNoMorePairs
}

type PsbtErrorUnexpectedUnsignedTx struct {
}

func NewPsbtErrorUnexpectedUnsignedTx() *PsbtError {
	return &PsbtError{err: &PsbtErrorUnexpectedUnsignedTx{}}
}

func (e PsbtErrorUnexpectedUnsignedTx) destroy() {
}

func (err PsbtErrorUnexpectedUnsignedTx) Error() string {
	return fmt.Sprint("UnexpectedUnsignedTx")
}

func (self PsbtErrorUnexpectedUnsignedTx) Is(target error) bool {
	return target == ErrPsbtErrorUnexpectedUnsignedTx
}

type PsbtErrorNonStandardSighashType struct {
	Sighash uint32
}

func NewPsbtErrorNonStandardSighashType(
	sighash uint32,
) *PsbtError {
	return &PsbtError{err: &PsbtErrorNonStandardSighashType{
		Sighash: sighash}}
}

func (e PsbtErrorNonStandardSighashType) destroy() {
	FfiDestroyerUint32{}.Destroy(e.Sighash)
}

func (err PsbtErrorNonStandardSighashType) Error() string {
	return fmt.Sprint("NonStandardSighashType",
		": ",

		"Sighash=",
		err.Sighash,
	)
}

func (self PsbtErrorNonStandardSighashType) Is(target error) bool {
	return target == ErrPsbtErrorNonStandardSighashType
}

type PsbtErrorInvalidHash struct {
	Hash string
}

func NewPsbtErrorInvalidHash(
	hash string,
) *PsbtError {
	return &PsbtError{err: &PsbtErrorInvalidHash{
		Hash: hash}}
}

func (e PsbtErrorInvalidHash) destroy() {
	FfiDestroyerString{}.Destroy(e.Hash)
}

func (err PsbtErrorInvalidHash) Error() string {
	return fmt.Sprint("InvalidHash",
		": ",

		"Hash=",
		err.Hash,
	)
}

func (self PsbtErrorInvalidHash) Is(target error) bool {
	return target == ErrPsbtErrorInvalidHash
}

type PsbtErrorInvalidPreimageHashPair struct {
}

func NewPsbtErrorInvalidPreimageHashPair() *PsbtError {
	return &PsbtError{err: &PsbtErrorInvalidPreimageHashPair{}}
}

func (e PsbtErrorInvalidPreimageHashPair) destroy() {
}

func (err PsbtErrorInvalidPreimageHashPair) Error() string {
	return fmt.Sprint("InvalidPreimageHashPair")
}

func (self PsbtErrorInvalidPreimageHashPair) Is(target error) bool {
	return target == ErrPsbtErrorInvalidPreimageHashPair
}

type PsbtErrorCombineInconsistentKeySources struct {
	Xpub string
}

func NewPsbtErrorCombineInconsistentKeySources(
	xpub string,
) *PsbtError {
	return &PsbtError{err: &PsbtErrorCombineInconsistentKeySources{
		Xpub: xpub}}
}

func (e PsbtErrorCombineInconsistentKeySources) destroy() {
	FfiDestroyerString{}.Destroy(e.Xpub)
}

func (err PsbtErrorCombineInconsistentKeySources) Error() string {
	return fmt.Sprint("CombineInconsistentKeySources",
		": ",

		"Xpub=",
		err.Xpub,
	)
}

func (self PsbtErrorCombineInconsistentKeySources) Is(target error) bool {
	return target == ErrPsbtErrorCombineInconsistentKeySources
}

type PsbtErrorConsensusEncoding struct {
	EncodingError string
}

func NewPsbtErrorConsensusEncoding(
	encodingError string,
) *PsbtError {
	return &PsbtError{err: &PsbtErrorConsensusEncoding{
		EncodingError: encodingError}}
}

func (e PsbtErrorConsensusEncoding) destroy() {
	FfiDestroyerString{}.Destroy(e.EncodingError)
}

func (err PsbtErrorConsensusEncoding) Error() string {
	return fmt.Sprint("ConsensusEncoding",
		": ",

		"EncodingError=",
		err.EncodingError,
	)
}

func (self PsbtErrorConsensusEncoding) Is(target error) bool {
	return target == ErrPsbtErrorConsensusEncoding
}

type PsbtErrorNegativeFee struct {
}

func NewPsbtErrorNegativeFee() *PsbtError {
	return &PsbtError{err: &PsbtErrorNegativeFee{}}
}

func (e PsbtErrorNegativeFee) destroy() {
}

func (err PsbtErrorNegativeFee) Error() string {
	return fmt.Sprint("NegativeFee")
}

func (self PsbtErrorNegativeFee) Is(target error) bool {
	return target == ErrPsbtErrorNegativeFee
}

type PsbtErrorFeeOverflow struct {
}

func NewPsbtErrorFeeOverflow() *PsbtError {
	return &PsbtError{err: &PsbtErrorFeeOverflow{}}
}

func (e PsbtErrorFeeOverflow) destroy() {
}

func (err PsbtErrorFeeOverflow) Error() string {
	return fmt.Sprint("FeeOverflow")
}

func (self PsbtErrorFeeOverflow) Is(target error) bool {
	return target == ErrPsbtErrorFeeOverflow
}

type PsbtErrorInvalidPublicKey struct {
	ErrorMessage string
}

func NewPsbtErrorInvalidPublicKey(
	errorMessage string,
) *PsbtError {
	return &PsbtError{err: &PsbtErrorInvalidPublicKey{
		ErrorMessage: errorMessage}}
}

func (e PsbtErrorInvalidPublicKey) destroy() {
	FfiDestroyerString{}.Destroy(e.ErrorMessage)
}

func (err PsbtErrorInvalidPublicKey) Error() string {
	return fmt.Sprint("InvalidPublicKey",
		": ",

		"ErrorMessage=",
		err.ErrorMessage,
	)
}

func (self PsbtErrorInvalidPublicKey) Is(target error) bool {
	return target == ErrPsbtErrorInvalidPublicKey
}

type PsbtErrorInvalidSecp256k1PublicKey struct {
	Secp256k1Error string
}

func NewPsbtErrorInvalidSecp256k1PublicKey(
	secp256k1Error string,
) *PsbtError {
	return &PsbtError{err: &PsbtErrorInvalidSecp256k1PublicKey{
		Secp256k1Error: secp256k1Error}}
}

func (e PsbtErrorInvalidSecp256k1PublicKey) destroy() {
	FfiDestroyerString{}.Destroy(e.Secp256k1Error)
}

func (err PsbtErrorInvalidSecp256k1PublicKey) Error() string {
	return fmt.Sprint("InvalidSecp256k1PublicKey",
		": ",

		"Secp256k1Error=",
		err.Secp256k1Error,
	)
}

func (self PsbtErrorInvalidSecp256k1PublicKey) Is(target error) bool {
	return target == ErrPsbtErrorInvalidSecp256k1PublicKey
}

type PsbtErrorInvalidXOnlyPublicKey struct {
}

func NewPsbtErrorInvalidXOnlyPublicKey() *PsbtError {
	return &PsbtError{err: &PsbtErrorInvalidXOnlyPublicKey{}}
}

func (e PsbtErrorInvalidXOnlyPublicKey) destroy() {
}

func (err PsbtErrorInvalidXOnlyPublicKey) Error() string {
	return fmt.Sprint("InvalidXOnlyPublicKey")
}

func (self PsbtErrorInvalidXOnlyPublicKey) Is(target error) bool {
	return target == ErrPsbtErrorInvalidXOnlyPublicKey
}

type PsbtErrorInvalidEcdsaSignature struct {
	ErrorMessage string
}

func NewPsbtErrorInvalidEcdsaSignature(
	errorMessage string,
) *PsbtError {
	return &PsbtError{err: &PsbtErrorInvalidEcdsaSignature{
		ErrorMessage: errorMessage}}
}

func (e PsbtErrorInvalidEcdsaSignature) destroy() {
	FfiDestroyerString{}.Destroy(e.ErrorMessage)
}

func (err PsbtErrorInvalidEcdsaSignature) Error() string {
	return fmt.Sprint("InvalidEcdsaSignature",
		": ",

		"ErrorMessage=",
		err.ErrorMessage,
	)
}

func (self PsbtErrorInvalidEcdsaSignature) Is(target error) bool {
	return target == ErrPsbtErrorInvalidEcdsaSignature
}

type PsbtErrorInvalidTaprootSignature struct {
	ErrorMessage string
}

func NewPsbtErrorInvalidTaprootSignature(
	errorMessage string,
) *PsbtError {
	return &PsbtError{err: &PsbtErrorInvalidTaprootSignature{
		ErrorMessage: errorMessage}}
}

func (e PsbtErrorInvalidTaprootSignature) destroy() {
	FfiDestroyerString{}.Destroy(e.ErrorMessage)
}

func (err PsbtErrorInvalidTaprootSignature) Error() string {
	return fmt.Sprint("InvalidTaprootSignature",
		": ",

		"ErrorMessage=",
		err.ErrorMessage,
	)
}

func (self PsbtErrorInvalidTaprootSignature) Is(target error) bool {
	return target == ErrPsbtErrorInvalidTaprootSignature
}

type PsbtErrorInvalidControlBlock struct {
}

func NewPsbtErrorInvalidControlBlock() *PsbtError {
	return &PsbtError{err: &PsbtErrorInvalidControlBlock{}}
}

func (e PsbtErrorInvalidControlBlock) destroy() {
}

func (err PsbtErrorInvalidControlBlock) Error() string {
	return fmt.Sprint("InvalidControlBlock")
}

func (self PsbtErrorInvalidControlBlock) Is(target error) bool {
	return target == ErrPsbtErrorInvalidControlBlock
}

type PsbtErrorInvalidLeafVersion struct {
}

func NewPsbtErrorInvalidLeafVersion() *PsbtError {
	return &PsbtError{err: &PsbtErrorInvalidLeafVersion{}}
}

func (e PsbtErrorInvalidLeafVersion) destroy() {
}

func (err PsbtErrorInvalidLeafVersion) Error() string {
	return fmt.Sprint("InvalidLeafVersion")
}

func (self PsbtErrorInvalidLeafVersion) Is(target error) bool {
	return target == ErrPsbtErrorInvalidLeafVersion
}

type PsbtErrorTaproot struct {
}

func NewPsbtErrorTaproot() *PsbtError {
	return &PsbtError{err: &PsbtErrorTaproot{}}
}

func (e PsbtErrorTaproot) destroy() {
}

func (err PsbtErrorTaproot) Error() string {
	return fmt.Sprint("Taproot")
}

func (self PsbtErrorTaproot) Is(target error) bool {
	return target == ErrPsbtErrorTaproot
}

type PsbtErrorTapTree struct {
	ErrorMessage string
}

func NewPsbtErrorTapTree(
	errorMessage string,
) *PsbtError {
	return &PsbtError{err: &PsbtErrorTapTree{
		ErrorMessage: errorMessage}}
}

func (e PsbtErrorTapTree) destroy() {
	FfiDestroyerString{}.Destroy(e.ErrorMessage)
}

func (err PsbtErrorTapTree) Error() string {
	return fmt.Sprint("TapTree",
		": ",

		"ErrorMessage=",
		err.ErrorMessage,
	)
}

func (self PsbtErrorTapTree) Is(target error) bool {
	return target == ErrPsbtErrorTapTree
}

type PsbtErrorXPubKey struct {
}

func NewPsbtErrorXPubKey() *PsbtError {
	return &PsbtError{err: &PsbtErrorXPubKey{}}
}

func (e PsbtErrorXPubKey) destroy() {
}

func (err PsbtErrorXPubKey) Error() string {
	return fmt.Sprint("XPubKey")
}

func (self PsbtErrorXPubKey) Is(target error) bool {
	return target == ErrPsbtErrorXPubKey
}

type PsbtErrorVersion struct {
	ErrorMessage string
}

func NewPsbtErrorVersion(
	errorMessage string,
) *PsbtError {
	return &PsbtError{err: &PsbtErrorVersion{
		ErrorMessage: errorMessage}}
}

func (e PsbtErrorVersion) destroy() {
	FfiDestroyerString{}.Destroy(e.ErrorMessage)
}

func (err PsbtErrorVersion) Error() string {
	return fmt.Sprint("Version",
		": ",

		"ErrorMessage=",
		err.ErrorMessage,
	)
}

func (self PsbtErrorVersion) Is(target error) bool {
	return target == ErrPsbtErrorVersion
}

type PsbtErrorPartialDataConsumption struct {
}

func NewPsbtErrorPartialDataConsumption() *PsbtError {
	return &PsbtError{err: &PsbtErrorPartialDataConsumption{}}
}

func (e PsbtErrorPartialDataConsumption) destroy() {
}

func (err PsbtErrorPartialDataConsumption) Error() string {
	return fmt.Sprint("PartialDataConsumption")
}

func (self PsbtErrorPartialDataConsumption) Is(target error) bool {
	return target == ErrPsbtErrorPartialDataConsumption
}

type PsbtErrorIo struct {
	ErrorMessage string
}

func NewPsbtErrorIo(
	errorMessage string,
) *PsbtError {
	return &PsbtError{err: &PsbtErrorIo{
		ErrorMessage: errorMessage}}
}

func (e PsbtErrorIo) destroy() {
	FfiDestroyerString{}.Destroy(e.ErrorMessage)
}

func (err PsbtErrorIo) Error() string {
	return fmt.Sprint("Io",
		": ",

		"ErrorMessage=",
		err.ErrorMessage,
	)
}

func (self PsbtErrorIo) Is(target error) bool {
	return target == ErrPsbtErrorIo
}

type PsbtErrorOtherPsbtErr struct {
}

func NewPsbtErrorOtherPsbtErr() *PsbtError {
	return &PsbtError{err: &PsbtErrorOtherPsbtErr{}}
}

func (e PsbtErrorOtherPsbtErr) destroy() {
}

func (err PsbtErrorOtherPsbtErr) Error() string {
	return fmt.Sprint("OtherPsbtErr")
}

func (self PsbtErrorOtherPsbtErr) Is(target error) bool {
	return target == ErrPsbtErrorOtherPsbtErr
}

type FfiConverterPsbtError struct{}

var FfiConverterPsbtErrorINSTANCE = FfiConverterPsbtError{}

func (c FfiConverterPsbtError) Lift(eb RustBufferI) *PsbtError {
	return LiftFromRustBuffer[*PsbtError](c, eb)
}

func (c FfiConverterPsbtError) Lower(value *PsbtError) C.RustBuffer {
	return LowerIntoRustBuffer[*PsbtError](c, value)
}

func (c FfiConverterPsbtError) Read(reader io.Reader) *PsbtError {
	errorID := readUint32(reader)

	switch errorID {
	case 1:
		return &PsbtError{&PsbtErrorInvalidMagic{}}
	case 2:
		return &PsbtError{&PsbtErrorMissingUtxo{}}
	case 3:
		return &PsbtError{&PsbtErrorInvalidSeparator{}}
	case 4:
		return &PsbtError{&PsbtErrorPsbtUtxoOutOfBounds{}}
	case 5:
		return &PsbtError{&PsbtErrorInvalidKey{
			Key: FfiConverterStringINSTANCE.Read(reader),
		}}
	case 6:
		return &PsbtError{&PsbtErrorInvalidProprietaryKey{}}
	case 7:
		return &PsbtError{&PsbtErrorDuplicateKey{
			Key: FfiConverterStringINSTANCE.Read(reader),
		}}
	case 8:
		return &PsbtError{&PsbtErrorUnsignedTxHasScriptSigs{}}
	case 9:
		return &PsbtError{&PsbtErrorUnsignedTxHasScriptWitnesses{}}
	case 10:
		return &PsbtError{&PsbtErrorMustHaveUnsignedTx{}}
	case 11:
		return &PsbtError{&PsbtErrorNoMorePairs{}}
	case 12:
		return &PsbtError{&PsbtErrorUnexpectedUnsignedTx{}}
	case 13:
		return &PsbtError{&PsbtErrorNonStandardSighashType{
			Sighash: FfiConverterUint32INSTANCE.Read(reader),
		}}
	case 14:
		return &PsbtError{&PsbtErrorInvalidHash{
			Hash: FfiConverterStringINSTANCE.Read(reader),
		}}
	case 15:
		return &PsbtError{&PsbtErrorInvalidPreimageHashPair{}}
	case 16:
		return &PsbtError{&PsbtErrorCombineInconsistentKeySources{
			Xpub: FfiConverterStringINSTANCE.Read(reader),
		}}
	case 17:
		return &PsbtError{&PsbtErrorConsensusEncoding{
			EncodingError: FfiConverterStringINSTANCE.Read(reader),
		}}
	case 18:
		return &PsbtError{&PsbtErrorNegativeFee{}}
	case 19:
		return &PsbtError{&PsbtErrorFeeOverflow{}}
	case 20:
		return &PsbtError{&PsbtErrorInvalidPublicKey{
			ErrorMessage: FfiConverterStringINSTANCE.Read(reader),
		}}
	case 21:
		return &PsbtError{&PsbtErrorInvalidSecp256k1PublicKey{
			Secp256k1Error: FfiConverterStringINSTANCE.Read(reader),
		}}
	case 22:
		return &PsbtError{&PsbtErrorInvalidXOnlyPublicKey{}}
	case 23:
		return &PsbtError{&PsbtErrorInvalidEcdsaSignature{
			ErrorMessage: FfiConverterStringINSTANCE.Read(reader),
		}}
	case 24:
		return &PsbtError{&PsbtErrorInvalidTaprootSignature{
			ErrorMessage: FfiConverterStringINSTANCE.Read(reader),
		}}
	case 25:
		return &PsbtError{&PsbtErrorInvalidControlBlock{}}
	case 26:
		return &PsbtError{&PsbtErrorInvalidLeafVersion{}}
	case 27:
		return &PsbtError{&PsbtErrorTaproot{}}
	case 28:
		return &PsbtError{&PsbtErrorTapTree{
			ErrorMessage: FfiConverterStringINSTANCE.Read(reader),
		}}
	case 29:
		return &PsbtError{&PsbtErrorXPubKey{}}
	case 30:
		return &PsbtError{&PsbtErrorVersion{
			ErrorMessage: FfiConverterStringINSTANCE.Read(reader),
		}}
	case 31:
		return &PsbtError{&PsbtErrorPartialDataConsumption{}}
	case 32:
		return &PsbtError{&PsbtErrorIo{
			ErrorMessage: FfiConverterStringINSTANCE.Read(reader),
		}}
	case 33:
		return &PsbtError{&PsbtErrorOtherPsbtErr{}}
	default:
		panic(fmt.Sprintf("Unknown error code %d in FfiConverterPsbtError.Read()", errorID))
	}
}

func (c FfiConverterPsbtError) Write(writer io.Writer, value *PsbtError) {
	switch variantValue := value.err.(type) {
	case *PsbtErrorInvalidMagic:
		writeInt32(writer, 1)
	case *PsbtErrorMissingUtxo:
		writeInt32(writer, 2)
	case *PsbtErrorInvalidSeparator:
		writeInt32(writer, 3)
	case *PsbtErrorPsbtUtxoOutOfBounds:
		writeInt32(writer, 4)
	case *PsbtErrorInvalidKey:
		writeInt32(writer, 5)
		FfiConverterStringINSTANCE.Write(writer, variantValue.Key)
	case *PsbtErrorInvalidProprietaryKey:
		writeInt32(writer, 6)
	case *PsbtErrorDuplicateKey:
		writeInt32(writer, 7)
		FfiConverterStringINSTANCE.Write(writer, variantValue.Key)
	case *PsbtErrorUnsignedTxHasScriptSigs:
		writeInt32(writer, 8)
	case *PsbtErrorUnsignedTxHasScriptWitnesses:
		writeInt32(writer, 9)
	case *PsbtErrorMustHaveUnsignedTx:
		writeInt32(writer, 10)
	case *PsbtErrorNoMorePairs:
		writeInt32(writer, 11)
	case *PsbtErrorUnexpectedUnsignedTx:
		writeInt32(writer, 12)
	case *PsbtErrorNonStandardSighashType:
		writeInt32(writer, 13)
		FfiConverterUint32INSTANCE.Write(writer, variantValue.Sighash)
	case *PsbtErrorInvalidHash:
		writeInt32(writer, 14)
		FfiConverterStringINSTANCE.Write(writer, variantValue.Hash)
	case *PsbtErrorInvalidPreimageHashPair:
		writeInt32(writer, 15)
	case *PsbtErrorCombineInconsistentKeySources:
		writeInt32(writer, 16)
		FfiConverterStringINSTANCE.Write(writer, variantValue.Xpub)
	case *PsbtErrorConsensusEncoding:
		writeInt32(writer, 17)
		FfiConverterStringINSTANCE.Write(writer, variantValue.EncodingError)
	case *PsbtErrorNegativeFee:
		writeInt32(writer, 18)
	case *PsbtErrorFeeOverflow:
		writeInt32(writer, 19)
	case *PsbtErrorInvalidPublicKey:
		writeInt32(writer, 20)
		FfiConverterStringINSTANCE.Write(writer, variantValue.ErrorMessage)
	case *PsbtErrorInvalidSecp256k1PublicKey:
		writeInt32(writer, 21)
		FfiConverterStringINSTANCE.Write(writer, variantValue.Secp256k1Error)
	case *PsbtErrorInvalidXOnlyPublicKey:
		writeInt32(writer, 22)
	case *PsbtErrorInvalidEcdsaSignature:
		writeInt32(writer, 23)
		FfiConverterStringINSTANCE.Write(writer, variantValue.ErrorMessage)
	case *PsbtErrorInvalidTaprootSignature:
		writeInt32(writer, 24)
		FfiConverterStringINSTANCE.Write(writer, variantValue.ErrorMessage)
	case *PsbtErrorInvalidControlBlock:
		writeInt32(writer, 25)
	case *PsbtErrorInvalidLeafVersion:
		writeInt32(writer, 26)
	case *PsbtErrorTaproot:
		writeInt32(writer, 27)
	case *PsbtErrorTapTree:
		writeInt32(writer, 28)
		FfiConverterStringINSTANCE.Write(writer, variantValue.ErrorMessage)
	case *PsbtErrorXPubKey:
		writeInt32(writer, 29)
	case *PsbtErrorVersion:
		writeInt32(writer, 30)
		FfiConverterStringINSTANCE.Write(writer, variantValue.ErrorMessage)
	case *PsbtErrorPartialDataConsumption:
		writeInt32(writer, 31)
	case *PsbtErrorIo:
		writeInt32(writer, 32)
		FfiConverterStringINSTANCE.Write(writer, variantValue.ErrorMessage)
	case *PsbtErrorOtherPsbtErr:
		writeInt32(writer, 33)
	default:
		_ = variantValue
		panic(fmt.Sprintf("invalid error value `%v` in FfiConverterPsbtError.Write", value))
	}
}

type FfiDestroyerPsbtError struct{}

func (_ FfiDestroyerPsbtError) Destroy(value *PsbtError) {
	switch variantValue := value.err.(type) {
	case PsbtErrorInvalidMagic:
		variantValue.destroy()
	case PsbtErrorMissingUtxo:
		variantValue.destroy()
	case PsbtErrorInvalidSeparator:
		variantValue.destroy()
	case PsbtErrorPsbtUtxoOutOfBounds:
		variantValue.destroy()
	case PsbtErrorInvalidKey:
		variantValue.destroy()
	case PsbtErrorInvalidProprietaryKey:
		variantValue.destroy()
	case PsbtErrorDuplicateKey:
		variantValue.destroy()
	case PsbtErrorUnsignedTxHasScriptSigs:
		variantValue.destroy()
	case PsbtErrorUnsignedTxHasScriptWitnesses:
		variantValue.destroy()
	case PsbtErrorMustHaveUnsignedTx:
		variantValue.destroy()
	case PsbtErrorNoMorePairs:
		variantValue.destroy()
	case PsbtErrorUnexpectedUnsignedTx:
		variantValue.destroy()
	case PsbtErrorNonStandardSighashType:
		variantValue.destroy()
	case PsbtErrorInvalidHash:
		variantValue.destroy()
	case PsbtErrorInvalidPreimageHashPair:
		variantValue.destroy()
	case PsbtErrorCombineInconsistentKeySources:
		variantValue.destroy()
	case PsbtErrorConsensusEncoding:
		variantValue.destroy()
	case PsbtErrorNegativeFee:
		variantValue.destroy()
	case PsbtErrorFeeOverflow:
		variantValue.destroy()
	case PsbtErrorInvalidPublicKey:
		variantValue.destroy()
	case PsbtErrorInvalidSecp256k1PublicKey:
		variantValue.destroy()
	case PsbtErrorInvalidXOnlyPublicKey:
		variantValue.destroy()
	case PsbtErrorInvalidEcdsaSignature:
		variantValue.destroy()
	case PsbtErrorInvalidTaprootSignature:
		variantValue.destroy()
	case PsbtErrorInvalidControlBlock:
		variantValue.destroy()
	case PsbtErrorInvalidLeafVersion:
		variantValue.destroy()
	case PsbtErrorTaproot:
		variantValue.destroy()
	case PsbtErrorTapTree:
		variantValue.destroy()
	case PsbtErrorXPubKey:
		variantValue.destroy()
	case PsbtErrorVersion:
		variantValue.destroy()
	case PsbtErrorPartialDataConsumption:
		variantValue.destroy()
	case PsbtErrorIo:
		variantValue.destroy()
	case PsbtErrorOtherPsbtErr:
		variantValue.destroy()
	default:
		_ = variantValue
		panic(fmt.Sprintf("invalid error value `%v` in FfiDestroyerPsbtError.Destroy", value))
	}
}

type PsbtFinalizeError struct {
	err error
}

// Convience method to turn *PsbtFinalizeError into error
// Avoiding treating nil pointer as non nil error interface
func (err *PsbtFinalizeError) AsError() error {
	if err == nil {
		return nil
	} else {
		return err
	}
}

func (err PsbtFinalizeError) Error() string {
	return fmt.Sprintf("PsbtFinalizeError: %s", err.err.Error())
}

func (err PsbtFinalizeError) Unwrap() error {
	return err.err
}

// Err* are used for checking error type with `errors.Is`
var ErrPsbtFinalizeErrorInputError = fmt.Errorf("PsbtFinalizeErrorInputError")
var ErrPsbtFinalizeErrorWrongInputCount = fmt.Errorf("PsbtFinalizeErrorWrongInputCount")
var ErrPsbtFinalizeErrorInputIdxOutofBounds = fmt.Errorf("PsbtFinalizeErrorInputIdxOutofBounds")

// Variant structs
type PsbtFinalizeErrorInputError struct {
	Reason string
	Index  uint32
}

func NewPsbtFinalizeErrorInputError(
	reason string,
	index uint32,
) *PsbtFinalizeError {
	return &PsbtFinalizeError{err: &PsbtFinalizeErrorInputError{
		Reason: reason,
		Index:  index}}
}

func (e PsbtFinalizeErrorInputError) destroy() {
	FfiDestroyerString{}.Destroy(e.Reason)
	FfiDestroyerUint32{}.Destroy(e.Index)
}

func (err PsbtFinalizeErrorInputError) Error() string {
	return fmt.Sprint("InputError",
		": ",

		"Reason=",
		err.Reason,
		", ",
		"Index=",
		err.Index,
	)
}

func (self PsbtFinalizeErrorInputError) Is(target error) bool {
	return target == ErrPsbtFinalizeErrorInputError
}

type PsbtFinalizeErrorWrongInputCount struct {
	InTx  uint32
	InMap uint32
}

func NewPsbtFinalizeErrorWrongInputCount(
	inTx uint32,
	inMap uint32,
) *PsbtFinalizeError {
	return &PsbtFinalizeError{err: &PsbtFinalizeErrorWrongInputCount{
		InTx:  inTx,
		InMap: inMap}}
}

func (e PsbtFinalizeErrorWrongInputCount) destroy() {
	FfiDestroyerUint32{}.Destroy(e.InTx)
	FfiDestroyerUint32{}.Destroy(e.InMap)
}

func (err PsbtFinalizeErrorWrongInputCount) Error() string {
	return fmt.Sprint("WrongInputCount",
		": ",

		"InTx=",
		err.InTx,
		", ",
		"InMap=",
		err.InMap,
	)
}

func (self PsbtFinalizeErrorWrongInputCount) Is(target error) bool {
	return target == ErrPsbtFinalizeErrorWrongInputCount
}

type PsbtFinalizeErrorInputIdxOutofBounds struct {
	PsbtInp   uint32
	Requested uint32
}

func NewPsbtFinalizeErrorInputIdxOutofBounds(
	psbtInp uint32,
	requested uint32,
) *PsbtFinalizeError {
	return &PsbtFinalizeError{err: &PsbtFinalizeErrorInputIdxOutofBounds{
		PsbtInp:   psbtInp,
		Requested: requested}}
}

func (e PsbtFinalizeErrorInputIdxOutofBounds) destroy() {
	FfiDestroyerUint32{}.Destroy(e.PsbtInp)
	FfiDestroyerUint32{}.Destroy(e.Requested)
}

func (err PsbtFinalizeErrorInputIdxOutofBounds) Error() string {
	return fmt.Sprint("InputIdxOutofBounds",
		": ",

		"PsbtInp=",
		err.PsbtInp,
		", ",
		"Requested=",
		err.Requested,
	)
}

func (self PsbtFinalizeErrorInputIdxOutofBounds) Is(target error) bool {
	return target == ErrPsbtFinalizeErrorInputIdxOutofBounds
}

type FfiConverterPsbtFinalizeError struct{}

var FfiConverterPsbtFinalizeErrorINSTANCE = FfiConverterPsbtFinalizeError{}

func (c FfiConverterPsbtFinalizeError) Lift(eb RustBufferI) *PsbtFinalizeError {
	return LiftFromRustBuffer[*PsbtFinalizeError](c, eb)
}

func (c FfiConverterPsbtFinalizeError) Lower(value *PsbtFinalizeError) C.RustBuffer {
	return LowerIntoRustBuffer[*PsbtFinalizeError](c, value)
}

func (c FfiConverterPsbtFinalizeError) Read(reader io.Reader) *PsbtFinalizeError {
	errorID := readUint32(reader)

	switch errorID {
	case 1:
		return &PsbtFinalizeError{&PsbtFinalizeErrorInputError{
			Reason: FfiConverterStringINSTANCE.Read(reader),
			Index:  FfiConverterUint32INSTANCE.Read(reader),
		}}
	case 2:
		return &PsbtFinalizeError{&PsbtFinalizeErrorWrongInputCount{
			InTx:  FfiConverterUint32INSTANCE.Read(reader),
			InMap: FfiConverterUint32INSTANCE.Read(reader),
		}}
	case 3:
		return &PsbtFinalizeError{&PsbtFinalizeErrorInputIdxOutofBounds{
			PsbtInp:   FfiConverterUint32INSTANCE.Read(reader),
			Requested: FfiConverterUint32INSTANCE.Read(reader),
		}}
	default:
		panic(fmt.Sprintf("Unknown error code %d in FfiConverterPsbtFinalizeError.Read()", errorID))
	}
}

func (c FfiConverterPsbtFinalizeError) Write(writer io.Writer, value *PsbtFinalizeError) {
	switch variantValue := value.err.(type) {
	case *PsbtFinalizeErrorInputError:
		writeInt32(writer, 1)
		FfiConverterStringINSTANCE.Write(writer, variantValue.Reason)
		FfiConverterUint32INSTANCE.Write(writer, variantValue.Index)
	case *PsbtFinalizeErrorWrongInputCount:
		writeInt32(writer, 2)
		FfiConverterUint32INSTANCE.Write(writer, variantValue.InTx)
		FfiConverterUint32INSTANCE.Write(writer, variantValue.InMap)
	case *PsbtFinalizeErrorInputIdxOutofBounds:
		writeInt32(writer, 3)
		FfiConverterUint32INSTANCE.Write(writer, variantValue.PsbtInp)
		FfiConverterUint32INSTANCE.Write(writer, variantValue.Requested)
	default:
		_ = variantValue
		panic(fmt.Sprintf("invalid error value `%v` in FfiConverterPsbtFinalizeError.Write", value))
	}
}

type FfiDestroyerPsbtFinalizeError struct{}

func (_ FfiDestroyerPsbtFinalizeError) Destroy(value *PsbtFinalizeError) {
	switch variantValue := value.err.(type) {
	case PsbtFinalizeErrorInputError:
		variantValue.destroy()
	case PsbtFinalizeErrorWrongInputCount:
		variantValue.destroy()
	case PsbtFinalizeErrorInputIdxOutofBounds:
		variantValue.destroy()
	default:
		_ = variantValue
		panic(fmt.Sprintf("invalid error value `%v` in FfiDestroyerPsbtFinalizeError.Destroy", value))
	}
}

type PsbtParseError struct {
	err error
}

// Convience method to turn *PsbtParseError into error
// Avoiding treating nil pointer as non nil error interface
func (err *PsbtParseError) AsError() error {
	if err == nil {
		return nil
	} else {
		return err
	}
}

func (err PsbtParseError) Error() string {
	return fmt.Sprintf("PsbtParseError: %s", err.err.Error())
}

func (err PsbtParseError) Unwrap() error {
	return err.err
}

// Err* are used for checking error type with `errors.Is`
var ErrPsbtParseErrorPsbtEncoding = fmt.Errorf("PsbtParseErrorPsbtEncoding")
var ErrPsbtParseErrorBase64Encoding = fmt.Errorf("PsbtParseErrorBase64Encoding")

// Variant structs
type PsbtParseErrorPsbtEncoding struct {
	ErrorMessage string
}

func NewPsbtParseErrorPsbtEncoding(
	errorMessage string,
) *PsbtParseError {
	return &PsbtParseError{err: &PsbtParseErrorPsbtEncoding{
		ErrorMessage: errorMessage}}
}

func (e PsbtParseErrorPsbtEncoding) destroy() {
	FfiDestroyerString{}.Destroy(e.ErrorMessage)
}

func (err PsbtParseErrorPsbtEncoding) Error() string {
	return fmt.Sprint("PsbtEncoding",
		": ",

		"ErrorMessage=",
		err.ErrorMessage,
	)
}

func (self PsbtParseErrorPsbtEncoding) Is(target error) bool {
	return target == ErrPsbtParseErrorPsbtEncoding
}

type PsbtParseErrorBase64Encoding struct {
	ErrorMessage string
}

func NewPsbtParseErrorBase64Encoding(
	errorMessage string,
) *PsbtParseError {
	return &PsbtParseError{err: &PsbtParseErrorBase64Encoding{
		ErrorMessage: errorMessage}}
}

func (e PsbtParseErrorBase64Encoding) destroy() {
	FfiDestroyerString{}.Destroy(e.ErrorMessage)
}

func (err PsbtParseErrorBase64Encoding) Error() string {
	return fmt.Sprint("Base64Encoding",
		": ",

		"ErrorMessage=",
		err.ErrorMessage,
	)
}

func (self PsbtParseErrorBase64Encoding) Is(target error) bool {
	return target == ErrPsbtParseErrorBase64Encoding
}

type FfiConverterPsbtParseError struct{}

var FfiConverterPsbtParseErrorINSTANCE = FfiConverterPsbtParseError{}

func (c FfiConverterPsbtParseError) Lift(eb RustBufferI) *PsbtParseError {
	return LiftFromRustBuffer[*PsbtParseError](c, eb)
}

func (c FfiConverterPsbtParseError) Lower(value *PsbtParseError) C.RustBuffer {
	return LowerIntoRustBuffer[*PsbtParseError](c, value)
}

func (c FfiConverterPsbtParseError) Read(reader io.Reader) *PsbtParseError {
	errorID := readUint32(reader)

	switch errorID {
	case 1:
		return &PsbtParseError{&PsbtParseErrorPsbtEncoding{
			ErrorMessage: FfiConverterStringINSTANCE.Read(reader),
		}}
	case 2:
		return &PsbtParseError{&PsbtParseErrorBase64Encoding{
			ErrorMessage: FfiConverterStringINSTANCE.Read(reader),
		}}
	default:
		panic(fmt.Sprintf("Unknown error code %d in FfiConverterPsbtParseError.Read()", errorID))
	}
}

func (c FfiConverterPsbtParseError) Write(writer io.Writer, value *PsbtParseError) {
	switch variantValue := value.err.(type) {
	case *PsbtParseErrorPsbtEncoding:
		writeInt32(writer, 1)
		FfiConverterStringINSTANCE.Write(writer, variantValue.ErrorMessage)
	case *PsbtParseErrorBase64Encoding:
		writeInt32(writer, 2)
		FfiConverterStringINSTANCE.Write(writer, variantValue.ErrorMessage)
	default:
		_ = variantValue
		panic(fmt.Sprintf("invalid error value `%v` in FfiConverterPsbtParseError.Write", value))
	}
}

type FfiDestroyerPsbtParseError struct{}

func (_ FfiDestroyerPsbtParseError) Destroy(value *PsbtParseError) {
	switch variantValue := value.err.(type) {
	case PsbtParseErrorPsbtEncoding:
		variantValue.destroy()
	case PsbtParseErrorBase64Encoding:
		variantValue.destroy()
	default:
		_ = variantValue
		panic(fmt.Sprintf("invalid error value `%v` in FfiDestroyerPsbtParseError.Destroy", value))
	}
}

type RequestBuilderError struct {
	err error
}

// Convience method to turn *RequestBuilderError into error
// Avoiding treating nil pointer as non nil error interface
func (err *RequestBuilderError) AsError() error {
	if err == nil {
		return nil
	} else {
		return err
	}
}

func (err RequestBuilderError) Error() string {
	return fmt.Sprintf("RequestBuilderError: %s", err.err.Error())
}

func (err RequestBuilderError) Unwrap() error {
	return err.err
}

// Err* are used for checking error type with `errors.Is`
var ErrRequestBuilderErrorRequestAlreadyConsumed = fmt.Errorf("RequestBuilderErrorRequestAlreadyConsumed")

// Variant structs
type RequestBuilderErrorRequestAlreadyConsumed struct {
}

func NewRequestBuilderErrorRequestAlreadyConsumed() *RequestBuilderError {
	return &RequestBuilderError{err: &RequestBuilderErrorRequestAlreadyConsumed{}}
}

func (e RequestBuilderErrorRequestAlreadyConsumed) destroy() {
}

func (err RequestBuilderErrorRequestAlreadyConsumed) Error() string {
	return fmt.Sprint("RequestAlreadyConsumed")
}

func (self RequestBuilderErrorRequestAlreadyConsumed) Is(target error) bool {
	return target == ErrRequestBuilderErrorRequestAlreadyConsumed
}

type FfiConverterRequestBuilderError struct{}

var FfiConverterRequestBuilderErrorINSTANCE = FfiConverterRequestBuilderError{}

func (c FfiConverterRequestBuilderError) Lift(eb RustBufferI) *RequestBuilderError {
	return LiftFromRustBuffer[*RequestBuilderError](c, eb)
}

func (c FfiConverterRequestBuilderError) Lower(value *RequestBuilderError) C.RustBuffer {
	return LowerIntoRustBuffer[*RequestBuilderError](c, value)
}

func (c FfiConverterRequestBuilderError) Read(reader io.Reader) *RequestBuilderError {
	errorID := readUint32(reader)

	switch errorID {
	case 1:
		return &RequestBuilderError{&RequestBuilderErrorRequestAlreadyConsumed{}}
	default:
		panic(fmt.Sprintf("Unknown error code %d in FfiConverterRequestBuilderError.Read()", errorID))
	}
}

func (c FfiConverterRequestBuilderError) Write(writer io.Writer, value *RequestBuilderError) {
	switch variantValue := value.err.(type) {
	case *RequestBuilderErrorRequestAlreadyConsumed:
		writeInt32(writer, 1)
	default:
		_ = variantValue
		panic(fmt.Sprintf("invalid error value `%v` in FfiConverterRequestBuilderError.Write", value))
	}
}

type FfiDestroyerRequestBuilderError struct{}

func (_ FfiDestroyerRequestBuilderError) Destroy(value *RequestBuilderError) {
	switch variantValue := value.err.(type) {
	case RequestBuilderErrorRequestAlreadyConsumed:
		variantValue.destroy()
	default:
		_ = variantValue
		panic(fmt.Sprintf("invalid error value `%v` in FfiDestroyerRequestBuilderError.Destroy", value))
	}
}

type Satisfaction interface {
	Destroy()
}
type SatisfactionPartial struct {
	N          uint64
	M          uint64
	Items      []uint64
	Sorted     *bool
	Conditions map[uint32][]Condition
}

func (e SatisfactionPartial) Destroy() {
	FfiDestroyerUint64{}.Destroy(e.N)
	FfiDestroyerUint64{}.Destroy(e.M)
	FfiDestroyerSequenceUint64{}.Destroy(e.Items)
	FfiDestroyerOptionalBool{}.Destroy(e.Sorted)
	FfiDestroyerMapUint32SequenceCondition{}.Destroy(e.Conditions)
}

type SatisfactionPartialComplete struct {
	N          uint64
	M          uint64
	Items      []uint64
	Sorted     *bool
	Conditions map[string][]Condition
}

func (e SatisfactionPartialComplete) Destroy() {
	FfiDestroyerUint64{}.Destroy(e.N)
	FfiDestroyerUint64{}.Destroy(e.M)
	FfiDestroyerSequenceUint64{}.Destroy(e.Items)
	FfiDestroyerOptionalBool{}.Destroy(e.Sorted)
	FfiDestroyerMapStringSequenceCondition{}.Destroy(e.Conditions)
}

type SatisfactionComplete struct {
	Condition Condition
}

func (e SatisfactionComplete) Destroy() {
	FfiDestroyerCondition{}.Destroy(e.Condition)
}

type SatisfactionNone struct {
	Msg string
}

func (e SatisfactionNone) Destroy() {
	FfiDestroyerString{}.Destroy(e.Msg)
}

type FfiConverterSatisfaction struct{}

var FfiConverterSatisfactionINSTANCE = FfiConverterSatisfaction{}

func (c FfiConverterSatisfaction) Lift(rb RustBufferI) Satisfaction {
	return LiftFromRustBuffer[Satisfaction](c, rb)
}

func (c FfiConverterSatisfaction) Lower(value Satisfaction) C.RustBuffer {
	return LowerIntoRustBuffer[Satisfaction](c, value)
}
func (FfiConverterSatisfaction) Read(reader io.Reader) Satisfaction {
	id := readInt32(reader)
	switch id {
	case 1:
		return SatisfactionPartial{
			FfiConverterUint64INSTANCE.Read(reader),
			FfiConverterUint64INSTANCE.Read(reader),
			FfiConverterSequenceUint64INSTANCE.Read(reader),
			FfiConverterOptionalBoolINSTANCE.Read(reader),
			FfiConverterMapUint32SequenceConditionINSTANCE.Read(reader),
		}
	case 2:
		return SatisfactionPartialComplete{
			FfiConverterUint64INSTANCE.Read(reader),
			FfiConverterUint64INSTANCE.Read(reader),
			FfiConverterSequenceUint64INSTANCE.Read(reader),
			FfiConverterOptionalBoolINSTANCE.Read(reader),
			FfiConverterMapStringSequenceConditionINSTANCE.Read(reader),
		}
	case 3:
		return SatisfactionComplete{
			FfiConverterConditionINSTANCE.Read(reader),
		}
	case 4:
		return SatisfactionNone{
			FfiConverterStringINSTANCE.Read(reader),
		}
	default:
		panic(fmt.Sprintf("invalid enum value %v in FfiConverterSatisfaction.Read()", id))
	}
}

func (FfiConverterSatisfaction) Write(writer io.Writer, value Satisfaction) {
	switch variant_value := value.(type) {
	case SatisfactionPartial:
		writeInt32(writer, 1)
		FfiConverterUint64INSTANCE.Write(writer, variant_value.N)
		FfiConverterUint64INSTANCE.Write(writer, variant_value.M)
		FfiConverterSequenceUint64INSTANCE.Write(writer, variant_value.Items)
		FfiConverterOptionalBoolINSTANCE.Write(writer, variant_value.Sorted)
		FfiConverterMapUint32SequenceConditionINSTANCE.Write(writer, variant_value.Conditions)
	case SatisfactionPartialComplete:
		writeInt32(writer, 2)
		FfiConverterUint64INSTANCE.Write(writer, variant_value.N)
		FfiConverterUint64INSTANCE.Write(writer, variant_value.M)
		FfiConverterSequenceUint64INSTANCE.Write(writer, variant_value.Items)
		FfiConverterOptionalBoolINSTANCE.Write(writer, variant_value.Sorted)
		FfiConverterMapStringSequenceConditionINSTANCE.Write(writer, variant_value.Conditions)
	case SatisfactionComplete:
		writeInt32(writer, 3)
		FfiConverterConditionINSTANCE.Write(writer, variant_value.Condition)
	case SatisfactionNone:
		writeInt32(writer, 4)
		FfiConverterStringINSTANCE.Write(writer, variant_value.Msg)
	default:
		_ = variant_value
		panic(fmt.Sprintf("invalid enum value `%v` in FfiConverterSatisfaction.Write", value))
	}
}

type FfiDestroyerSatisfaction struct{}

func (_ FfiDestroyerSatisfaction) Destroy(value Satisfaction) {
	value.Destroy()
}

type SatisfiableItem interface {
	Destroy()
}
type SatisfiableItemEcdsaSignature struct {
	Key PkOrF
}

func (e SatisfiableItemEcdsaSignature) Destroy() {
	FfiDestroyerPkOrF{}.Destroy(e.Key)
}

type SatisfiableItemSchnorrSignature struct {
	Key PkOrF
}

func (e SatisfiableItemSchnorrSignature) Destroy() {
	FfiDestroyerPkOrF{}.Destroy(e.Key)
}

type SatisfiableItemSha256Preimage struct {
	Hash string
}

func (e SatisfiableItemSha256Preimage) Destroy() {
	FfiDestroyerString{}.Destroy(e.Hash)
}

type SatisfiableItemHash256Preimage struct {
	Hash string
}

func (e SatisfiableItemHash256Preimage) Destroy() {
	FfiDestroyerString{}.Destroy(e.Hash)
}

type SatisfiableItemRipemd160Preimage struct {
	Hash string
}

func (e SatisfiableItemRipemd160Preimage) Destroy() {
	FfiDestroyerString{}.Destroy(e.Hash)
}

type SatisfiableItemHash160Preimage struct {
	Hash string
}

func (e SatisfiableItemHash160Preimage) Destroy() {
	FfiDestroyerString{}.Destroy(e.Hash)
}

type SatisfiableItemAbsoluteTimelock struct {
	Value LockTime
}

func (e SatisfiableItemAbsoluteTimelock) Destroy() {
	FfiDestroyerLockTime{}.Destroy(e.Value)
}

type SatisfiableItemRelativeTimelock struct {
	Value uint32
}

func (e SatisfiableItemRelativeTimelock) Destroy() {
	FfiDestroyerUint32{}.Destroy(e.Value)
}

type SatisfiableItemMultisig struct {
	Keys      []PkOrF
	Threshold uint64
}

func (e SatisfiableItemMultisig) Destroy() {
	FfiDestroyerSequencePkOrF{}.Destroy(e.Keys)
	FfiDestroyerUint64{}.Destroy(e.Threshold)
}

type SatisfiableItemThresh struct {
	Items     []*Policy
	Threshold uint64
}

func (e SatisfiableItemThresh) Destroy() {
	FfiDestroyerSequencePolicy{}.Destroy(e.Items)
	FfiDestroyerUint64{}.Destroy(e.Threshold)
}

type FfiConverterSatisfiableItem struct{}

var FfiConverterSatisfiableItemINSTANCE = FfiConverterSatisfiableItem{}

func (c FfiConverterSatisfiableItem) Lift(rb RustBufferI) SatisfiableItem {
	return LiftFromRustBuffer[SatisfiableItem](c, rb)
}

func (c FfiConverterSatisfiableItem) Lower(value SatisfiableItem) C.RustBuffer {
	return LowerIntoRustBuffer[SatisfiableItem](c, value)
}
func (FfiConverterSatisfiableItem) Read(reader io.Reader) SatisfiableItem {
	id := readInt32(reader)
	switch id {
	case 1:
		return SatisfiableItemEcdsaSignature{
			FfiConverterPkOrFINSTANCE.Read(reader),
		}
	case 2:
		return SatisfiableItemSchnorrSignature{
			FfiConverterPkOrFINSTANCE.Read(reader),
		}
	case 3:
		return SatisfiableItemSha256Preimage{
			FfiConverterStringINSTANCE.Read(reader),
		}
	case 4:
		return SatisfiableItemHash256Preimage{
			FfiConverterStringINSTANCE.Read(reader),
		}
	case 5:
		return SatisfiableItemRipemd160Preimage{
			FfiConverterStringINSTANCE.Read(reader),
		}
	case 6:
		return SatisfiableItemHash160Preimage{
			FfiConverterStringINSTANCE.Read(reader),
		}
	case 7:
		return SatisfiableItemAbsoluteTimelock{
			FfiConverterLockTimeINSTANCE.Read(reader),
		}
	case 8:
		return SatisfiableItemRelativeTimelock{
			FfiConverterUint32INSTANCE.Read(reader),
		}
	case 9:
		return SatisfiableItemMultisig{
			FfiConverterSequencePkOrFINSTANCE.Read(reader),
			FfiConverterUint64INSTANCE.Read(reader),
		}
	case 10:
		return SatisfiableItemThresh{
			FfiConverterSequencePolicyINSTANCE.Read(reader),
			FfiConverterUint64INSTANCE.Read(reader),
		}
	default:
		panic(fmt.Sprintf("invalid enum value %v in FfiConverterSatisfiableItem.Read()", id))
	}
}

func (FfiConverterSatisfiableItem) Write(writer io.Writer, value SatisfiableItem) {
	switch variant_value := value.(type) {
	case SatisfiableItemEcdsaSignature:
		writeInt32(writer, 1)
		FfiConverterPkOrFINSTANCE.Write(writer, variant_value.Key)
	case SatisfiableItemSchnorrSignature:
		writeInt32(writer, 2)
		FfiConverterPkOrFINSTANCE.Write(writer, variant_value.Key)
	case SatisfiableItemSha256Preimage:
		writeInt32(writer, 3)
		FfiConverterStringINSTANCE.Write(writer, variant_value.Hash)
	case SatisfiableItemHash256Preimage:
		writeInt32(writer, 4)
		FfiConverterStringINSTANCE.Write(writer, variant_value.Hash)
	case SatisfiableItemRipemd160Preimage:
		writeInt32(writer, 5)
		FfiConverterStringINSTANCE.Write(writer, variant_value.Hash)
	case SatisfiableItemHash160Preimage:
		writeInt32(writer, 6)
		FfiConverterStringINSTANCE.Write(writer, variant_value.Hash)
	case SatisfiableItemAbsoluteTimelock:
		writeInt32(writer, 7)
		FfiConverterLockTimeINSTANCE.Write(writer, variant_value.Value)
	case SatisfiableItemRelativeTimelock:
		writeInt32(writer, 8)
		FfiConverterUint32INSTANCE.Write(writer, variant_value.Value)
	case SatisfiableItemMultisig:
		writeInt32(writer, 9)
		FfiConverterSequencePkOrFINSTANCE.Write(writer, variant_value.Keys)
		FfiConverterUint64INSTANCE.Write(writer, variant_value.Threshold)
	case SatisfiableItemThresh:
		writeInt32(writer, 10)
		FfiConverterSequencePolicyINSTANCE.Write(writer, variant_value.Items)
		FfiConverterUint64INSTANCE.Write(writer, variant_value.Threshold)
	default:
		_ = variant_value
		panic(fmt.Sprintf("invalid enum value `%v` in FfiConverterSatisfiableItem.Write", value))
	}
}

type FfiDestroyerSatisfiableItem struct{}

func (_ FfiDestroyerSatisfiableItem) Destroy(value SatisfiableItem) {
	value.Destroy()
}

type SignerError struct {
	err error
}

// Convience method to turn *SignerError into error
// Avoiding treating nil pointer as non nil error interface
func (err *SignerError) AsError() error {
	if err == nil {
		return nil
	} else {
		return err
	}
}

func (err SignerError) Error() string {
	return fmt.Sprintf("SignerError: %s", err.err.Error())
}

func (err SignerError) Unwrap() error {
	return err.err
}

// Err* are used for checking error type with `errors.Is`
var ErrSignerErrorMissingKey = fmt.Errorf("SignerErrorMissingKey")
var ErrSignerErrorInvalidKey = fmt.Errorf("SignerErrorInvalidKey")
var ErrSignerErrorUserCanceled = fmt.Errorf("SignerErrorUserCanceled")
var ErrSignerErrorInputIndexOutOfRange = fmt.Errorf("SignerErrorInputIndexOutOfRange")
var ErrSignerErrorMissingNonWitnessUtxo = fmt.Errorf("SignerErrorMissingNonWitnessUtxo")
var ErrSignerErrorInvalidNonWitnessUtxo = fmt.Errorf("SignerErrorInvalidNonWitnessUtxo")
var ErrSignerErrorMissingWitnessUtxo = fmt.Errorf("SignerErrorMissingWitnessUtxo")
var ErrSignerErrorMissingWitnessScript = fmt.Errorf("SignerErrorMissingWitnessScript")
var ErrSignerErrorMissingHdKeypath = fmt.Errorf("SignerErrorMissingHdKeypath")
var ErrSignerErrorNonStandardSighash = fmt.Errorf("SignerErrorNonStandardSighash")
var ErrSignerErrorInvalidSighash = fmt.Errorf("SignerErrorInvalidSighash")
var ErrSignerErrorSighashP2wpkh = fmt.Errorf("SignerErrorSighashP2wpkh")
var ErrSignerErrorSighashTaproot = fmt.Errorf("SignerErrorSighashTaproot")
var ErrSignerErrorTxInputsIndexError = fmt.Errorf("SignerErrorTxInputsIndexError")
var ErrSignerErrorMiniscriptPsbt = fmt.Errorf("SignerErrorMiniscriptPsbt")
var ErrSignerErrorExternal = fmt.Errorf("SignerErrorExternal")
var ErrSignerErrorPsbt = fmt.Errorf("SignerErrorPsbt")

// Variant structs
type SignerErrorMissingKey struct {
}

func NewSignerErrorMissingKey() *SignerError {
	return &SignerError{err: &SignerErrorMissingKey{}}
}

func (e SignerErrorMissingKey) destroy() {
}

func (err SignerErrorMissingKey) Error() string {
	return fmt.Sprint("MissingKey")
}

func (self SignerErrorMissingKey) Is(target error) bool {
	return target == ErrSignerErrorMissingKey
}

type SignerErrorInvalidKey struct {
}

func NewSignerErrorInvalidKey() *SignerError {
	return &SignerError{err: &SignerErrorInvalidKey{}}
}

func (e SignerErrorInvalidKey) destroy() {
}

func (err SignerErrorInvalidKey) Error() string {
	return fmt.Sprint("InvalidKey")
}

func (self SignerErrorInvalidKey) Is(target error) bool {
	return target == ErrSignerErrorInvalidKey
}

type SignerErrorUserCanceled struct {
}

func NewSignerErrorUserCanceled() *SignerError {
	return &SignerError{err: &SignerErrorUserCanceled{}}
}

func (e SignerErrorUserCanceled) destroy() {
}

func (err SignerErrorUserCanceled) Error() string {
	return fmt.Sprint("UserCanceled")
}

func (self SignerErrorUserCanceled) Is(target error) bool {
	return target == ErrSignerErrorUserCanceled
}

type SignerErrorInputIndexOutOfRange struct {
}

func NewSignerErrorInputIndexOutOfRange() *SignerError {
	return &SignerError{err: &SignerErrorInputIndexOutOfRange{}}
}

func (e SignerErrorInputIndexOutOfRange) destroy() {
}

func (err SignerErrorInputIndexOutOfRange) Error() string {
	return fmt.Sprint("InputIndexOutOfRange")
}

func (self SignerErrorInputIndexOutOfRange) Is(target error) bool {
	return target == ErrSignerErrorInputIndexOutOfRange
}

type SignerErrorMissingNonWitnessUtxo struct {
}

func NewSignerErrorMissingNonWitnessUtxo() *SignerError {
	return &SignerError{err: &SignerErrorMissingNonWitnessUtxo{}}
}

func (e SignerErrorMissingNonWitnessUtxo) destroy() {
}

func (err SignerErrorMissingNonWitnessUtxo) Error() string {
	return fmt.Sprint("MissingNonWitnessUtxo")
}

func (self SignerErrorMissingNonWitnessUtxo) Is(target error) bool {
	return target == ErrSignerErrorMissingNonWitnessUtxo
}

type SignerErrorInvalidNonWitnessUtxo struct {
}

func NewSignerErrorInvalidNonWitnessUtxo() *SignerError {
	return &SignerError{err: &SignerErrorInvalidNonWitnessUtxo{}}
}

func (e SignerErrorInvalidNonWitnessUtxo) destroy() {
}

func (err SignerErrorInvalidNonWitnessUtxo) Error() string {
	return fmt.Sprint("InvalidNonWitnessUtxo")
}

func (self SignerErrorInvalidNonWitnessUtxo) Is(target error) bool {
	return target == ErrSignerErrorInvalidNonWitnessUtxo
}

type SignerErrorMissingWitnessUtxo struct {
}

func NewSignerErrorMissingWitnessUtxo() *SignerError {
	return &SignerError{err: &SignerErrorMissingWitnessUtxo{}}
}

func (e SignerErrorMissingWitnessUtxo) destroy() {
}

func (err SignerErrorMissingWitnessUtxo) Error() string {
	return fmt.Sprint("MissingWitnessUtxo")
}

func (self SignerErrorMissingWitnessUtxo) Is(target error) bool {
	return target == ErrSignerErrorMissingWitnessUtxo
}

type SignerErrorMissingWitnessScript struct {
}

func NewSignerErrorMissingWitnessScript() *SignerError {
	return &SignerError{err: &SignerErrorMissingWitnessScript{}}
}

func (e SignerErrorMissingWitnessScript) destroy() {
}

func (err SignerErrorMissingWitnessScript) Error() string {
	return fmt.Sprint("MissingWitnessScript")
}

func (self SignerErrorMissingWitnessScript) Is(target error) bool {
	return target == ErrSignerErrorMissingWitnessScript
}

type SignerErrorMissingHdKeypath struct {
}

func NewSignerErrorMissingHdKeypath() *SignerError {
	return &SignerError{err: &SignerErrorMissingHdKeypath{}}
}

func (e SignerErrorMissingHdKeypath) destroy() {
}

func (err SignerErrorMissingHdKeypath) Error() string {
	return fmt.Sprint("MissingHdKeypath")
}

func (self SignerErrorMissingHdKeypath) Is(target error) bool {
	return target == ErrSignerErrorMissingHdKeypath
}

type SignerErrorNonStandardSighash struct {
}

func NewSignerErrorNonStandardSighash() *SignerError {
	return &SignerError{err: &SignerErrorNonStandardSighash{}}
}

func (e SignerErrorNonStandardSighash) destroy() {
}

func (err SignerErrorNonStandardSighash) Error() string {
	return fmt.Sprint("NonStandardSighash")
}

func (self SignerErrorNonStandardSighash) Is(target error) bool {
	return target == ErrSignerErrorNonStandardSighash
}

type SignerErrorInvalidSighash struct {
}

func NewSignerErrorInvalidSighash() *SignerError {
	return &SignerError{err: &SignerErrorInvalidSighash{}}
}

func (e SignerErrorInvalidSighash) destroy() {
}

func (err SignerErrorInvalidSighash) Error() string {
	return fmt.Sprint("InvalidSighash")
}

func (self SignerErrorInvalidSighash) Is(target error) bool {
	return target == ErrSignerErrorInvalidSighash
}

type SignerErrorSighashP2wpkh struct {
	ErrorMessage string
}

func NewSignerErrorSighashP2wpkh(
	errorMessage string,
) *SignerError {
	return &SignerError{err: &SignerErrorSighashP2wpkh{
		ErrorMessage: errorMessage}}
}

func (e SignerErrorSighashP2wpkh) destroy() {
	FfiDestroyerString{}.Destroy(e.ErrorMessage)
}

func (err SignerErrorSighashP2wpkh) Error() string {
	return fmt.Sprint("SighashP2wpkh",
		": ",

		"ErrorMessage=",
		err.ErrorMessage,
	)
}

func (self SignerErrorSighashP2wpkh) Is(target error) bool {
	return target == ErrSignerErrorSighashP2wpkh
}

type SignerErrorSighashTaproot struct {
	ErrorMessage string
}

func NewSignerErrorSighashTaproot(
	errorMessage string,
) *SignerError {
	return &SignerError{err: &SignerErrorSighashTaproot{
		ErrorMessage: errorMessage}}
}

func (e SignerErrorSighashTaproot) destroy() {
	FfiDestroyerString{}.Destroy(e.ErrorMessage)
}

func (err SignerErrorSighashTaproot) Error() string {
	return fmt.Sprint("SighashTaproot",
		": ",

		"ErrorMessage=",
		err.ErrorMessage,
	)
}

func (self SignerErrorSighashTaproot) Is(target error) bool {
	return target == ErrSignerErrorSighashTaproot
}

type SignerErrorTxInputsIndexError struct {
	ErrorMessage string
}

func NewSignerErrorTxInputsIndexError(
	errorMessage string,
) *SignerError {
	return &SignerError{err: &SignerErrorTxInputsIndexError{
		ErrorMessage: errorMessage}}
}

func (e SignerErrorTxInputsIndexError) destroy() {
	FfiDestroyerString{}.Destroy(e.ErrorMessage)
}

func (err SignerErrorTxInputsIndexError) Error() string {
	return fmt.Sprint("TxInputsIndexError",
		": ",

		"ErrorMessage=",
		err.ErrorMessage,
	)
}

func (self SignerErrorTxInputsIndexError) Is(target error) bool {
	return target == ErrSignerErrorTxInputsIndexError
}

type SignerErrorMiniscriptPsbt struct {
	ErrorMessage string
}

func NewSignerErrorMiniscriptPsbt(
	errorMessage string,
) *SignerError {
	return &SignerError{err: &SignerErrorMiniscriptPsbt{
		ErrorMessage: errorMessage}}
}

func (e SignerErrorMiniscriptPsbt) destroy() {
	FfiDestroyerString{}.Destroy(e.ErrorMessage)
}

func (err SignerErrorMiniscriptPsbt) Error() string {
	return fmt.Sprint("MiniscriptPsbt",
		": ",

		"ErrorMessage=",
		err.ErrorMessage,
	)
}

func (self SignerErrorMiniscriptPsbt) Is(target error) bool {
	return target == ErrSignerErrorMiniscriptPsbt
}

type SignerErrorExternal struct {
	ErrorMessage string
}

func NewSignerErrorExternal(
	errorMessage string,
) *SignerError {
	return &SignerError{err: &SignerErrorExternal{
		ErrorMessage: errorMessage}}
}

func (e SignerErrorExternal) destroy() {
	FfiDestroyerString{}.Destroy(e.ErrorMessage)
}

func (err SignerErrorExternal) Error() string {
	return fmt.Sprint("External",
		": ",

		"ErrorMessage=",
		err.ErrorMessage,
	)
}

func (self SignerErrorExternal) Is(target error) bool {
	return target == ErrSignerErrorExternal
}

type SignerErrorPsbt struct {
	ErrorMessage string
}

func NewSignerErrorPsbt(
	errorMessage string,
) *SignerError {
	return &SignerError{err: &SignerErrorPsbt{
		ErrorMessage: errorMessage}}
}

func (e SignerErrorPsbt) destroy() {
	FfiDestroyerString{}.Destroy(e.ErrorMessage)
}

func (err SignerErrorPsbt) Error() string {
	return fmt.Sprint("Psbt",
		": ",

		"ErrorMessage=",
		err.ErrorMessage,
	)
}

func (self SignerErrorPsbt) Is(target error) bool {
	return target == ErrSignerErrorPsbt
}

type FfiConverterSignerError struct{}

var FfiConverterSignerErrorINSTANCE = FfiConverterSignerError{}

func (c FfiConverterSignerError) Lift(eb RustBufferI) *SignerError {
	return LiftFromRustBuffer[*SignerError](c, eb)
}

func (c FfiConverterSignerError) Lower(value *SignerError) C.RustBuffer {
	return LowerIntoRustBuffer[*SignerError](c, value)
}

func (c FfiConverterSignerError) Read(reader io.Reader) *SignerError {
	errorID := readUint32(reader)

	switch errorID {
	case 1:
		return &SignerError{&SignerErrorMissingKey{}}
	case 2:
		return &SignerError{&SignerErrorInvalidKey{}}
	case 3:
		return &SignerError{&SignerErrorUserCanceled{}}
	case 4:
		return &SignerError{&SignerErrorInputIndexOutOfRange{}}
	case 5:
		return &SignerError{&SignerErrorMissingNonWitnessUtxo{}}
	case 6:
		return &SignerError{&SignerErrorInvalidNonWitnessUtxo{}}
	case 7:
		return &SignerError{&SignerErrorMissingWitnessUtxo{}}
	case 8:
		return &SignerError{&SignerErrorMissingWitnessScript{}}
	case 9:
		return &SignerError{&SignerErrorMissingHdKeypath{}}
	case 10:
		return &SignerError{&SignerErrorNonStandardSighash{}}
	case 11:
		return &SignerError{&SignerErrorInvalidSighash{}}
	case 12:
		return &SignerError{&SignerErrorSighashP2wpkh{
			ErrorMessage: FfiConverterStringINSTANCE.Read(reader),
		}}
	case 13:
		return &SignerError{&SignerErrorSighashTaproot{
			ErrorMessage: FfiConverterStringINSTANCE.Read(reader),
		}}
	case 14:
		return &SignerError{&SignerErrorTxInputsIndexError{
			ErrorMessage: FfiConverterStringINSTANCE.Read(reader),
		}}
	case 15:
		return &SignerError{&SignerErrorMiniscriptPsbt{
			ErrorMessage: FfiConverterStringINSTANCE.Read(reader),
		}}
	case 16:
		return &SignerError{&SignerErrorExternal{
			ErrorMessage: FfiConverterStringINSTANCE.Read(reader),
		}}
	case 17:
		return &SignerError{&SignerErrorPsbt{
			ErrorMessage: FfiConverterStringINSTANCE.Read(reader),
		}}
	default:
		panic(fmt.Sprintf("Unknown error code %d in FfiConverterSignerError.Read()", errorID))
	}
}

func (c FfiConverterSignerError) Write(writer io.Writer, value *SignerError) {
	switch variantValue := value.err.(type) {
	case *SignerErrorMissingKey:
		writeInt32(writer, 1)
	case *SignerErrorInvalidKey:
		writeInt32(writer, 2)
	case *SignerErrorUserCanceled:
		writeInt32(writer, 3)
	case *SignerErrorInputIndexOutOfRange:
		writeInt32(writer, 4)
	case *SignerErrorMissingNonWitnessUtxo:
		writeInt32(writer, 5)
	case *SignerErrorInvalidNonWitnessUtxo:
		writeInt32(writer, 6)
	case *SignerErrorMissingWitnessUtxo:
		writeInt32(writer, 7)
	case *SignerErrorMissingWitnessScript:
		writeInt32(writer, 8)
	case *SignerErrorMissingHdKeypath:
		writeInt32(writer, 9)
	case *SignerErrorNonStandardSighash:
		writeInt32(writer, 10)
	case *SignerErrorInvalidSighash:
		writeInt32(writer, 11)
	case *SignerErrorSighashP2wpkh:
		writeInt32(writer, 12)
		FfiConverterStringINSTANCE.Write(writer, variantValue.ErrorMessage)
	case *SignerErrorSighashTaproot:
		writeInt32(writer, 13)
		FfiConverterStringINSTANCE.Write(writer, variantValue.ErrorMessage)
	case *SignerErrorTxInputsIndexError:
		writeInt32(writer, 14)
		FfiConverterStringINSTANCE.Write(writer, variantValue.ErrorMessage)
	case *SignerErrorMiniscriptPsbt:
		writeInt32(writer, 15)
		FfiConverterStringINSTANCE.Write(writer, variantValue.ErrorMessage)
	case *SignerErrorExternal:
		writeInt32(writer, 16)
		FfiConverterStringINSTANCE.Write(writer, variantValue.ErrorMessage)
	case *SignerErrorPsbt:
		writeInt32(writer, 17)
		FfiConverterStringINSTANCE.Write(writer, variantValue.ErrorMessage)
	default:
		_ = variantValue
		panic(fmt.Sprintf("invalid error value `%v` in FfiConverterSignerError.Write", value))
	}
}

type FfiDestroyerSignerError struct{}

func (_ FfiDestroyerSignerError) Destroy(value *SignerError) {
	switch variantValue := value.err.(type) {
	case SignerErrorMissingKey:
		variantValue.destroy()
	case SignerErrorInvalidKey:
		variantValue.destroy()
	case SignerErrorUserCanceled:
		variantValue.destroy()
	case SignerErrorInputIndexOutOfRange:
		variantValue.destroy()
	case SignerErrorMissingNonWitnessUtxo:
		variantValue.destroy()
	case SignerErrorInvalidNonWitnessUtxo:
		variantValue.destroy()
	case SignerErrorMissingWitnessUtxo:
		variantValue.destroy()
	case SignerErrorMissingWitnessScript:
		variantValue.destroy()
	case SignerErrorMissingHdKeypath:
		variantValue.destroy()
	case SignerErrorNonStandardSighash:
		variantValue.destroy()
	case SignerErrorInvalidSighash:
		variantValue.destroy()
	case SignerErrorSighashP2wpkh:
		variantValue.destroy()
	case SignerErrorSighashTaproot:
		variantValue.destroy()
	case SignerErrorTxInputsIndexError:
		variantValue.destroy()
	case SignerErrorMiniscriptPsbt:
		variantValue.destroy()
	case SignerErrorExternal:
		variantValue.destroy()
	case SignerErrorPsbt:
		variantValue.destroy()
	default:
		_ = variantValue
		panic(fmt.Sprintf("invalid error value `%v` in FfiDestroyerSignerError.Destroy", value))
	}
}

type SqliteError struct {
	err error
}

// Convience method to turn *SqliteError into error
// Avoiding treating nil pointer as non nil error interface
func (err *SqliteError) AsError() error {
	if err == nil {
		return nil
	} else {
		return err
	}
}

func (err SqliteError) Error() string {
	return fmt.Sprintf("SqliteError: %s", err.err.Error())
}

func (err SqliteError) Unwrap() error {
	return err.err
}

// Err* are used for checking error type with `errors.Is`
var ErrSqliteErrorSqlite = fmt.Errorf("SqliteErrorSqlite")

// Variant structs
type SqliteErrorSqlite struct {
	RusqliteError string
}

func NewSqliteErrorSqlite(
	rusqliteError string,
) *SqliteError {
	return &SqliteError{err: &SqliteErrorSqlite{
		RusqliteError: rusqliteError}}
}

func (e SqliteErrorSqlite) destroy() {
	FfiDestroyerString{}.Destroy(e.RusqliteError)
}

func (err SqliteErrorSqlite) Error() string {
	return fmt.Sprint("Sqlite",
		": ",

		"RusqliteError=",
		err.RusqliteError,
	)
}

func (self SqliteErrorSqlite) Is(target error) bool {
	return target == ErrSqliteErrorSqlite
}

type FfiConverterSqliteError struct{}

var FfiConverterSqliteErrorINSTANCE = FfiConverterSqliteError{}

func (c FfiConverterSqliteError) Lift(eb RustBufferI) *SqliteError {
	return LiftFromRustBuffer[*SqliteError](c, eb)
}

func (c FfiConverterSqliteError) Lower(value *SqliteError) C.RustBuffer {
	return LowerIntoRustBuffer[*SqliteError](c, value)
}

func (c FfiConverterSqliteError) Read(reader io.Reader) *SqliteError {
	errorID := readUint32(reader)

	switch errorID {
	case 1:
		return &SqliteError{&SqliteErrorSqlite{
			RusqliteError: FfiConverterStringINSTANCE.Read(reader),
		}}
	default:
		panic(fmt.Sprintf("Unknown error code %d in FfiConverterSqliteError.Read()", errorID))
	}
}

func (c FfiConverterSqliteError) Write(writer io.Writer, value *SqliteError) {
	switch variantValue := value.err.(type) {
	case *SqliteErrorSqlite:
		writeInt32(writer, 1)
		FfiConverterStringINSTANCE.Write(writer, variantValue.RusqliteError)
	default:
		_ = variantValue
		panic(fmt.Sprintf("invalid error value `%v` in FfiConverterSqliteError.Write", value))
	}
}

type FfiDestroyerSqliteError struct{}

func (_ FfiDestroyerSqliteError) Destroy(value *SqliteError) {
	switch variantValue := value.err.(type) {
	case SqliteErrorSqlite:
		variantValue.destroy()
	default:
		_ = variantValue
		panic(fmt.Sprintf("invalid error value `%v` in FfiDestroyerSqliteError.Destroy", value))
	}
}

type TransactionError struct {
	err error
}

// Convience method to turn *TransactionError into error
// Avoiding treating nil pointer as non nil error interface
func (err *TransactionError) AsError() error {
	if err == nil {
		return nil
	} else {
		return err
	}
}

func (err TransactionError) Error() string {
	return fmt.Sprintf("TransactionError: %s", err.err.Error())
}

func (err TransactionError) Unwrap() error {
	return err.err
}

// Err* are used for checking error type with `errors.Is`
var ErrTransactionErrorIo = fmt.Errorf("TransactionErrorIo")
var ErrTransactionErrorOversizedVectorAllocation = fmt.Errorf("TransactionErrorOversizedVectorAllocation")
var ErrTransactionErrorInvalidChecksum = fmt.Errorf("TransactionErrorInvalidChecksum")
var ErrTransactionErrorNonMinimalVarInt = fmt.Errorf("TransactionErrorNonMinimalVarInt")
var ErrTransactionErrorParseFailed = fmt.Errorf("TransactionErrorParseFailed")
var ErrTransactionErrorUnsupportedSegwitFlag = fmt.Errorf("TransactionErrorUnsupportedSegwitFlag")
var ErrTransactionErrorOtherTransactionErr = fmt.Errorf("TransactionErrorOtherTransactionErr")

// Variant structs
type TransactionErrorIo struct {
}

func NewTransactionErrorIo() *TransactionError {
	return &TransactionError{err: &TransactionErrorIo{}}
}

func (e TransactionErrorIo) destroy() {
}

func (err TransactionErrorIo) Error() string {
	return fmt.Sprint("Io")
}

func (self TransactionErrorIo) Is(target error) bool {
	return target == ErrTransactionErrorIo
}

type TransactionErrorOversizedVectorAllocation struct {
}

func NewTransactionErrorOversizedVectorAllocation() *TransactionError {
	return &TransactionError{err: &TransactionErrorOversizedVectorAllocation{}}
}

func (e TransactionErrorOversizedVectorAllocation) destroy() {
}

func (err TransactionErrorOversizedVectorAllocation) Error() string {
	return fmt.Sprint("OversizedVectorAllocation")
}

func (self TransactionErrorOversizedVectorAllocation) Is(target error) bool {
	return target == ErrTransactionErrorOversizedVectorAllocation
}

type TransactionErrorInvalidChecksum struct {
	Expected string
	Actual   string
}

func NewTransactionErrorInvalidChecksum(
	expected string,
	actual string,
) *TransactionError {
	return &TransactionError{err: &TransactionErrorInvalidChecksum{
		Expected: expected,
		Actual:   actual}}
}

func (e TransactionErrorInvalidChecksum) destroy() {
	FfiDestroyerString{}.Destroy(e.Expected)
	FfiDestroyerString{}.Destroy(e.Actual)
}

func (err TransactionErrorInvalidChecksum) Error() string {
	return fmt.Sprint("InvalidChecksum",
		": ",

		"Expected=",
		err.Expected,
		", ",
		"Actual=",
		err.Actual,
	)
}

func (self TransactionErrorInvalidChecksum) Is(target error) bool {
	return target == ErrTransactionErrorInvalidChecksum
}

type TransactionErrorNonMinimalVarInt struct {
}

func NewTransactionErrorNonMinimalVarInt() *TransactionError {
	return &TransactionError{err: &TransactionErrorNonMinimalVarInt{}}
}

func (e TransactionErrorNonMinimalVarInt) destroy() {
}

func (err TransactionErrorNonMinimalVarInt) Error() string {
	return fmt.Sprint("NonMinimalVarInt")
}

func (self TransactionErrorNonMinimalVarInt) Is(target error) bool {
	return target == ErrTransactionErrorNonMinimalVarInt
}

type TransactionErrorParseFailed struct {
}

func NewTransactionErrorParseFailed() *TransactionError {
	return &TransactionError{err: &TransactionErrorParseFailed{}}
}

func (e TransactionErrorParseFailed) destroy() {
}

func (err TransactionErrorParseFailed) Error() string {
	return fmt.Sprint("ParseFailed")
}

func (self TransactionErrorParseFailed) Is(target error) bool {
	return target == ErrTransactionErrorParseFailed
}

type TransactionErrorUnsupportedSegwitFlag struct {
	Flag uint8
}

func NewTransactionErrorUnsupportedSegwitFlag(
	flag uint8,
) *TransactionError {
	return &TransactionError{err: &TransactionErrorUnsupportedSegwitFlag{
		Flag: flag}}
}

func (e TransactionErrorUnsupportedSegwitFlag) destroy() {
	FfiDestroyerUint8{}.Destroy(e.Flag)
}

func (err TransactionErrorUnsupportedSegwitFlag) Error() string {
	return fmt.Sprint("UnsupportedSegwitFlag",
		": ",

		"Flag=",
		err.Flag,
	)
}

func (self TransactionErrorUnsupportedSegwitFlag) Is(target error) bool {
	return target == ErrTransactionErrorUnsupportedSegwitFlag
}

type TransactionErrorOtherTransactionErr struct {
}

func NewTransactionErrorOtherTransactionErr() *TransactionError {
	return &TransactionError{err: &TransactionErrorOtherTransactionErr{}}
}

func (e TransactionErrorOtherTransactionErr) destroy() {
}

func (err TransactionErrorOtherTransactionErr) Error() string {
	return fmt.Sprint("OtherTransactionErr")
}

func (self TransactionErrorOtherTransactionErr) Is(target error) bool {
	return target == ErrTransactionErrorOtherTransactionErr
}

type FfiConverterTransactionError struct{}

var FfiConverterTransactionErrorINSTANCE = FfiConverterTransactionError{}

func (c FfiConverterTransactionError) Lift(eb RustBufferI) *TransactionError {
	return LiftFromRustBuffer[*TransactionError](c, eb)
}

func (c FfiConverterTransactionError) Lower(value *TransactionError) C.RustBuffer {
	return LowerIntoRustBuffer[*TransactionError](c, value)
}

func (c FfiConverterTransactionError) Read(reader io.Reader) *TransactionError {
	errorID := readUint32(reader)

	switch errorID {
	case 1:
		return &TransactionError{&TransactionErrorIo{}}
	case 2:
		return &TransactionError{&TransactionErrorOversizedVectorAllocation{}}
	case 3:
		return &TransactionError{&TransactionErrorInvalidChecksum{
			Expected: FfiConverterStringINSTANCE.Read(reader),
			Actual:   FfiConverterStringINSTANCE.Read(reader),
		}}
	case 4:
		return &TransactionError{&TransactionErrorNonMinimalVarInt{}}
	case 5:
		return &TransactionError{&TransactionErrorParseFailed{}}
	case 6:
		return &TransactionError{&TransactionErrorUnsupportedSegwitFlag{
			Flag: FfiConverterUint8INSTANCE.Read(reader),
		}}
	case 7:
		return &TransactionError{&TransactionErrorOtherTransactionErr{}}
	default:
		panic(fmt.Sprintf("Unknown error code %d in FfiConverterTransactionError.Read()", errorID))
	}
}

func (c FfiConverterTransactionError) Write(writer io.Writer, value *TransactionError) {
	switch variantValue := value.err.(type) {
	case *TransactionErrorIo:
		writeInt32(writer, 1)
	case *TransactionErrorOversizedVectorAllocation:
		writeInt32(writer, 2)
	case *TransactionErrorInvalidChecksum:
		writeInt32(writer, 3)
		FfiConverterStringINSTANCE.Write(writer, variantValue.Expected)
		FfiConverterStringINSTANCE.Write(writer, variantValue.Actual)
	case *TransactionErrorNonMinimalVarInt:
		writeInt32(writer, 4)
	case *TransactionErrorParseFailed:
		writeInt32(writer, 5)
	case *TransactionErrorUnsupportedSegwitFlag:
		writeInt32(writer, 6)
		FfiConverterUint8INSTANCE.Write(writer, variantValue.Flag)
	case *TransactionErrorOtherTransactionErr:
		writeInt32(writer, 7)
	default:
		_ = variantValue
		panic(fmt.Sprintf("invalid error value `%v` in FfiConverterTransactionError.Write", value))
	}
}

type FfiDestroyerTransactionError struct{}

func (_ FfiDestroyerTransactionError) Destroy(value *TransactionError) {
	switch variantValue := value.err.(type) {
	case TransactionErrorIo:
		variantValue.destroy()
	case TransactionErrorOversizedVectorAllocation:
		variantValue.destroy()
	case TransactionErrorInvalidChecksum:
		variantValue.destroy()
	case TransactionErrorNonMinimalVarInt:
		variantValue.destroy()
	case TransactionErrorParseFailed:
		variantValue.destroy()
	case TransactionErrorUnsupportedSegwitFlag:
		variantValue.destroy()
	case TransactionErrorOtherTransactionErr:
		variantValue.destroy()
	default:
		_ = variantValue
		panic(fmt.Sprintf("invalid error value `%v` in FfiDestroyerTransactionError.Destroy", value))
	}
}

type TxidParseError struct {
	err error
}

// Convience method to turn *TxidParseError into error
// Avoiding treating nil pointer as non nil error interface
func (err *TxidParseError) AsError() error {
	if err == nil {
		return nil
	} else {
		return err
	}
}

func (err TxidParseError) Error() string {
	return fmt.Sprintf("TxidParseError: %s", err.err.Error())
}

func (err TxidParseError) Unwrap() error {
	return err.err
}

// Err* are used for checking error type with `errors.Is`
var ErrTxidParseErrorInvalidTxid = fmt.Errorf("TxidParseErrorInvalidTxid")

// Variant structs
type TxidParseErrorInvalidTxid struct {
	Txid string
}

func NewTxidParseErrorInvalidTxid(
	txid string,
) *TxidParseError {
	return &TxidParseError{err: &TxidParseErrorInvalidTxid{
		Txid: txid}}
}

func (e TxidParseErrorInvalidTxid) destroy() {
	FfiDestroyerString{}.Destroy(e.Txid)
}

func (err TxidParseErrorInvalidTxid) Error() string {
	return fmt.Sprint("InvalidTxid",
		": ",

		"Txid=",
		err.Txid,
	)
}

func (self TxidParseErrorInvalidTxid) Is(target error) bool {
	return target == ErrTxidParseErrorInvalidTxid
}

type FfiConverterTxidParseError struct{}

var FfiConverterTxidParseErrorINSTANCE = FfiConverterTxidParseError{}

func (c FfiConverterTxidParseError) Lift(eb RustBufferI) *TxidParseError {
	return LiftFromRustBuffer[*TxidParseError](c, eb)
}

func (c FfiConverterTxidParseError) Lower(value *TxidParseError) C.RustBuffer {
	return LowerIntoRustBuffer[*TxidParseError](c, value)
}

func (c FfiConverterTxidParseError) Read(reader io.Reader) *TxidParseError {
	errorID := readUint32(reader)

	switch errorID {
	case 1:
		return &TxidParseError{&TxidParseErrorInvalidTxid{
			Txid: FfiConverterStringINSTANCE.Read(reader),
		}}
	default:
		panic(fmt.Sprintf("Unknown error code %d in FfiConverterTxidParseError.Read()", errorID))
	}
}

func (c FfiConverterTxidParseError) Write(writer io.Writer, value *TxidParseError) {
	switch variantValue := value.err.(type) {
	case *TxidParseErrorInvalidTxid:
		writeInt32(writer, 1)
		FfiConverterStringINSTANCE.Write(writer, variantValue.Txid)
	default:
		_ = variantValue
		panic(fmt.Sprintf("invalid error value `%v` in FfiConverterTxidParseError.Write", value))
	}
}

type FfiDestroyerTxidParseError struct{}

func (_ FfiDestroyerTxidParseError) Destroy(value *TxidParseError) {
	switch variantValue := value.err.(type) {
	case TxidParseErrorInvalidTxid:
		variantValue.destroy()
	default:
		_ = variantValue
		panic(fmt.Sprintf("invalid error value `%v` in FfiDestroyerTxidParseError.Destroy", value))
	}
}

type WordCount uint

const (
	WordCountWords12 WordCount = 1
	WordCountWords15 WordCount = 2
	WordCountWords18 WordCount = 3
	WordCountWords21 WordCount = 4
	WordCountWords24 WordCount = 5
)

type FfiConverterWordCount struct{}

var FfiConverterWordCountINSTANCE = FfiConverterWordCount{}

func (c FfiConverterWordCount) Lift(rb RustBufferI) WordCount {
	return LiftFromRustBuffer[WordCount](c, rb)
}

func (c FfiConverterWordCount) Lower(value WordCount) C.RustBuffer {
	return LowerIntoRustBuffer[WordCount](c, value)
}
func (FfiConverterWordCount) Read(reader io.Reader) WordCount {
	id := readInt32(reader)
	return WordCount(id)
}

func (FfiConverterWordCount) Write(writer io.Writer, value WordCount) {
	writeInt32(writer, int32(value))
}

type FfiDestroyerWordCount struct{}

func (_ FfiDestroyerWordCount) Destroy(value WordCount) {
}

type FfiConverterOptionalUint32 struct{}

var FfiConverterOptionalUint32INSTANCE = FfiConverterOptionalUint32{}

func (c FfiConverterOptionalUint32) Lift(rb RustBufferI) *uint32 {
	return LiftFromRustBuffer[*uint32](c, rb)
}

func (_ FfiConverterOptionalUint32) Read(reader io.Reader) *uint32 {
	if readInt8(reader) == 0 {
		return nil
	}
	temp := FfiConverterUint32INSTANCE.Read(reader)
	return &temp
}

func (c FfiConverterOptionalUint32) Lower(value *uint32) C.RustBuffer {
	return LowerIntoRustBuffer[*uint32](c, value)
}

func (_ FfiConverterOptionalUint32) Write(writer io.Writer, value *uint32) {
	if value == nil {
		writeInt8(writer, 0)
	} else {
		writeInt8(writer, 1)
		FfiConverterUint32INSTANCE.Write(writer, *value)
	}
}

type FfiDestroyerOptionalUint32 struct{}

func (_ FfiDestroyerOptionalUint32) Destroy(value *uint32) {
	if value != nil {
		FfiDestroyerUint32{}.Destroy(*value)
	}
}

type FfiConverterOptionalUint64 struct{}

var FfiConverterOptionalUint64INSTANCE = FfiConverterOptionalUint64{}

func (c FfiConverterOptionalUint64) Lift(rb RustBufferI) *uint64 {
	return LiftFromRustBuffer[*uint64](c, rb)
}

func (_ FfiConverterOptionalUint64) Read(reader io.Reader) *uint64 {
	if readInt8(reader) == 0 {
		return nil
	}
	temp := FfiConverterUint64INSTANCE.Read(reader)
	return &temp
}

func (c FfiConverterOptionalUint64) Lower(value *uint64) C.RustBuffer {
	return LowerIntoRustBuffer[*uint64](c, value)
}

func (_ FfiConverterOptionalUint64) Write(writer io.Writer, value *uint64) {
	if value == nil {
		writeInt8(writer, 0)
	} else {
		writeInt8(writer, 1)
		FfiConverterUint64INSTANCE.Write(writer, *value)
	}
}

type FfiDestroyerOptionalUint64 struct{}

func (_ FfiDestroyerOptionalUint64) Destroy(value *uint64) {
	if value != nil {
		FfiDestroyerUint64{}.Destroy(*value)
	}
}

type FfiConverterOptionalInt64 struct{}

var FfiConverterOptionalInt64INSTANCE = FfiConverterOptionalInt64{}

func (c FfiConverterOptionalInt64) Lift(rb RustBufferI) *int64 {
	return LiftFromRustBuffer[*int64](c, rb)
}

func (_ FfiConverterOptionalInt64) Read(reader io.Reader) *int64 {
	if readInt8(reader) == 0 {
		return nil
	}
	temp := FfiConverterInt64INSTANCE.Read(reader)
	return &temp
}

func (c FfiConverterOptionalInt64) Lower(value *int64) C.RustBuffer {
	return LowerIntoRustBuffer[*int64](c, value)
}

func (_ FfiConverterOptionalInt64) Write(writer io.Writer, value *int64) {
	if value == nil {
		writeInt8(writer, 0)
	} else {
		writeInt8(writer, 1)
		FfiConverterInt64INSTANCE.Write(writer, *value)
	}
}

type FfiDestroyerOptionalInt64 struct{}

func (_ FfiDestroyerOptionalInt64) Destroy(value *int64) {
	if value != nil {
		FfiDestroyerInt64{}.Destroy(*value)
	}
}

type FfiConverterOptionalBool struct{}

var FfiConverterOptionalBoolINSTANCE = FfiConverterOptionalBool{}

func (c FfiConverterOptionalBool) Lift(rb RustBufferI) *bool {
	return LiftFromRustBuffer[*bool](c, rb)
}

func (_ FfiConverterOptionalBool) Read(reader io.Reader) *bool {
	if readInt8(reader) == 0 {
		return nil
	}
	temp := FfiConverterBoolINSTANCE.Read(reader)
	return &temp
}

func (c FfiConverterOptionalBool) Lower(value *bool) C.RustBuffer {
	return LowerIntoRustBuffer[*bool](c, value)
}

func (_ FfiConverterOptionalBool) Write(writer io.Writer, value *bool) {
	if value == nil {
		writeInt8(writer, 0)
	} else {
		writeInt8(writer, 1)
		FfiConverterBoolINSTANCE.Write(writer, *value)
	}
}

type FfiDestroyerOptionalBool struct{}

func (_ FfiDestroyerOptionalBool) Destroy(value *bool) {
	if value != nil {
		FfiDestroyerBool{}.Destroy(*value)
	}
}

type FfiConverterOptionalString struct{}

var FfiConverterOptionalStringINSTANCE = FfiConverterOptionalString{}

func (c FfiConverterOptionalString) Lift(rb RustBufferI) *string {
	return LiftFromRustBuffer[*string](c, rb)
}

func (_ FfiConverterOptionalString) Read(reader io.Reader) *string {
	if readInt8(reader) == 0 {
		return nil
	}
	temp := FfiConverterStringINSTANCE.Read(reader)
	return &temp
}

func (c FfiConverterOptionalString) Lower(value *string) C.RustBuffer {
	return LowerIntoRustBuffer[*string](c, value)
}

func (_ FfiConverterOptionalString) Write(writer io.Writer, value *string) {
	if value == nil {
		writeInt8(writer, 0)
	} else {
		writeInt8(writer, 1)
		FfiConverterStringINSTANCE.Write(writer, *value)
	}
}

type FfiDestroyerOptionalString struct{}

func (_ FfiDestroyerOptionalString) Destroy(value *string) {
	if value != nil {
		FfiDestroyerString{}.Destroy(*value)
	}
}

type FfiConverterOptionalPolicy struct{}

var FfiConverterOptionalPolicyINSTANCE = FfiConverterOptionalPolicy{}

func (c FfiConverterOptionalPolicy) Lift(rb RustBufferI) **Policy {
	return LiftFromRustBuffer[**Policy](c, rb)
}

func (_ FfiConverterOptionalPolicy) Read(reader io.Reader) **Policy {
	if readInt8(reader) == 0 {
		return nil
	}
	temp := FfiConverterPolicyINSTANCE.Read(reader)
	return &temp
}

func (c FfiConverterOptionalPolicy) Lower(value **Policy) C.RustBuffer {
	return LowerIntoRustBuffer[**Policy](c, value)
}

func (_ FfiConverterOptionalPolicy) Write(writer io.Writer, value **Policy) {
	if value == nil {
		writeInt8(writer, 0)
	} else {
		writeInt8(writer, 1)
		FfiConverterPolicyINSTANCE.Write(writer, *value)
	}
}

type FfiDestroyerOptionalPolicy struct{}

func (_ FfiDestroyerOptionalPolicy) Destroy(value **Policy) {
	if value != nil {
		FfiDestroyerPolicy{}.Destroy(*value)
	}
}

type FfiConverterOptionalTransaction struct{}

var FfiConverterOptionalTransactionINSTANCE = FfiConverterOptionalTransaction{}

func (c FfiConverterOptionalTransaction) Lift(rb RustBufferI) **Transaction {
	return LiftFromRustBuffer[**Transaction](c, rb)
}

func (_ FfiConverterOptionalTransaction) Read(reader io.Reader) **Transaction {
	if readInt8(reader) == 0 {
		return nil
	}
	temp := FfiConverterTransactionINSTANCE.Read(reader)
	return &temp
}

func (c FfiConverterOptionalTransaction) Lower(value **Transaction) C.RustBuffer {
	return LowerIntoRustBuffer[**Transaction](c, value)
}

func (_ FfiConverterOptionalTransaction) Write(writer io.Writer, value **Transaction) {
	if value == nil {
		writeInt8(writer, 0)
	} else {
		writeInt8(writer, 1)
		FfiConverterTransactionINSTANCE.Write(writer, *value)
	}
}

type FfiDestroyerOptionalTransaction struct{}

func (_ FfiDestroyerOptionalTransaction) Destroy(value **Transaction) {
	if value != nil {
		FfiDestroyerTransaction{}.Destroy(*value)
	}
}

type FfiConverterOptionalCanonicalTx struct{}

var FfiConverterOptionalCanonicalTxINSTANCE = FfiConverterOptionalCanonicalTx{}

func (c FfiConverterOptionalCanonicalTx) Lift(rb RustBufferI) *CanonicalTx {
	return LiftFromRustBuffer[*CanonicalTx](c, rb)
}

func (_ FfiConverterOptionalCanonicalTx) Read(reader io.Reader) *CanonicalTx {
	if readInt8(reader) == 0 {
		return nil
	}
	temp := FfiConverterCanonicalTxINSTANCE.Read(reader)
	return &temp
}

func (c FfiConverterOptionalCanonicalTx) Lower(value *CanonicalTx) C.RustBuffer {
	return LowerIntoRustBuffer[*CanonicalTx](c, value)
}

func (_ FfiConverterOptionalCanonicalTx) Write(writer io.Writer, value *CanonicalTx) {
	if value == nil {
		writeInt8(writer, 0)
	} else {
		writeInt8(writer, 1)
		FfiConverterCanonicalTxINSTANCE.Write(writer, *value)
	}
}

type FfiDestroyerOptionalCanonicalTx struct{}

func (_ FfiDestroyerOptionalCanonicalTx) Destroy(value *CanonicalTx) {
	if value != nil {
		FfiDestroyerCanonicalTx{}.Destroy(*value)
	}
}

type FfiConverterOptionalKeychainAndIndex struct{}

var FfiConverterOptionalKeychainAndIndexINSTANCE = FfiConverterOptionalKeychainAndIndex{}

func (c FfiConverterOptionalKeychainAndIndex) Lift(rb RustBufferI) *KeychainAndIndex {
	return LiftFromRustBuffer[*KeychainAndIndex](c, rb)
}

func (_ FfiConverterOptionalKeychainAndIndex) Read(reader io.Reader) *KeychainAndIndex {
	if readInt8(reader) == 0 {
		return nil
	}
	temp := FfiConverterKeychainAndIndexINSTANCE.Read(reader)
	return &temp
}

func (c FfiConverterOptionalKeychainAndIndex) Lower(value *KeychainAndIndex) C.RustBuffer {
	return LowerIntoRustBuffer[*KeychainAndIndex](c, value)
}

func (_ FfiConverterOptionalKeychainAndIndex) Write(writer io.Writer, value *KeychainAndIndex) {
	if value == nil {
		writeInt8(writer, 0)
	} else {
		writeInt8(writer, 1)
		FfiConverterKeychainAndIndexINSTANCE.Write(writer, *value)
	}
}

type FfiDestroyerOptionalKeychainAndIndex struct{}

func (_ FfiDestroyerOptionalKeychainAndIndex) Destroy(value *KeychainAndIndex) {
	if value != nil {
		FfiDestroyerKeychainAndIndex{}.Destroy(*value)
	}
}

type FfiConverterOptionalLocalOutput struct{}

var FfiConverterOptionalLocalOutputINSTANCE = FfiConverterOptionalLocalOutput{}

func (c FfiConverterOptionalLocalOutput) Lift(rb RustBufferI) *LocalOutput {
	return LiftFromRustBuffer[*LocalOutput](c, rb)
}

func (_ FfiConverterOptionalLocalOutput) Read(reader io.Reader) *LocalOutput {
	if readInt8(reader) == 0 {
		return nil
	}
	temp := FfiConverterLocalOutputINSTANCE.Read(reader)
	return &temp
}

func (c FfiConverterOptionalLocalOutput) Lower(value *LocalOutput) C.RustBuffer {
	return LowerIntoRustBuffer[*LocalOutput](c, value)
}

func (_ FfiConverterOptionalLocalOutput) Write(writer io.Writer, value *LocalOutput) {
	if value == nil {
		writeInt8(writer, 0)
	} else {
		writeInt8(writer, 1)
		FfiConverterLocalOutputINSTANCE.Write(writer, *value)
	}
}

type FfiDestroyerOptionalLocalOutput struct{}

func (_ FfiDestroyerOptionalLocalOutput) Destroy(value *LocalOutput) {
	if value != nil {
		FfiDestroyerLocalOutput{}.Destroy(*value)
	}
}

type FfiConverterOptionalSignOptions struct{}

var FfiConverterOptionalSignOptionsINSTANCE = FfiConverterOptionalSignOptions{}

func (c FfiConverterOptionalSignOptions) Lift(rb RustBufferI) *SignOptions {
	return LiftFromRustBuffer[*SignOptions](c, rb)
}

func (_ FfiConverterOptionalSignOptions) Read(reader io.Reader) *SignOptions {
	if readInt8(reader) == 0 {
		return nil
	}
	temp := FfiConverterSignOptionsINSTANCE.Read(reader)
	return &temp
}

func (c FfiConverterOptionalSignOptions) Lower(value *SignOptions) C.RustBuffer {
	return LowerIntoRustBuffer[*SignOptions](c, value)
}

func (_ FfiConverterOptionalSignOptions) Write(writer io.Writer, value *SignOptions) {
	if value == nil {
		writeInt8(writer, 0)
	} else {
		writeInt8(writer, 1)
		FfiConverterSignOptionsINSTANCE.Write(writer, *value)
	}
}

type FfiDestroyerOptionalSignOptions struct{}

func (_ FfiDestroyerOptionalSignOptions) Destroy(value *SignOptions) {
	if value != nil {
		FfiDestroyerSignOptions{}.Destroy(*value)
	}
}

type FfiConverterOptionalTx struct{}

var FfiConverterOptionalTxINSTANCE = FfiConverterOptionalTx{}

func (c FfiConverterOptionalTx) Lift(rb RustBufferI) *Tx {
	return LiftFromRustBuffer[*Tx](c, rb)
}

func (_ FfiConverterOptionalTx) Read(reader io.Reader) *Tx {
	if readInt8(reader) == 0 {
		return nil
	}
	temp := FfiConverterTxINSTANCE.Read(reader)
	return &temp
}

func (c FfiConverterOptionalTx) Lower(value *Tx) C.RustBuffer {
	return LowerIntoRustBuffer[*Tx](c, value)
}

func (_ FfiConverterOptionalTx) Write(writer io.Writer, value *Tx) {
	if value == nil {
		writeInt8(writer, 0)
	} else {
		writeInt8(writer, 1)
		FfiConverterTxINSTANCE.Write(writer, *value)
	}
}

type FfiDestroyerOptionalTx struct{}

func (_ FfiDestroyerOptionalTx) Destroy(value *Tx) {
	if value != nil {
		FfiDestroyerTx{}.Destroy(*value)
	}
}

type FfiConverterOptionalLockTime struct{}

var FfiConverterOptionalLockTimeINSTANCE = FfiConverterOptionalLockTime{}

func (c FfiConverterOptionalLockTime) Lift(rb RustBufferI) *LockTime {
	return LiftFromRustBuffer[*LockTime](c, rb)
}

func (_ FfiConverterOptionalLockTime) Read(reader io.Reader) *LockTime {
	if readInt8(reader) == 0 {
		return nil
	}
	temp := FfiConverterLockTimeINSTANCE.Read(reader)
	return &temp
}

func (c FfiConverterOptionalLockTime) Lower(value *LockTime) C.RustBuffer {
	return LowerIntoRustBuffer[*LockTime](c, value)
}

func (_ FfiConverterOptionalLockTime) Write(writer io.Writer, value *LockTime) {
	if value == nil {
		writeInt8(writer, 0)
	} else {
		writeInt8(writer, 1)
		FfiConverterLockTimeINSTANCE.Write(writer, *value)
	}
}

type FfiDestroyerOptionalLockTime struct{}

func (_ FfiDestroyerOptionalLockTime) Destroy(value *LockTime) {
	if value != nil {
		FfiDestroyerLockTime{}.Destroy(*value)
	}
}

type FfiConverterOptionalSequencePsbtFinalizeError struct{}

var FfiConverterOptionalSequencePsbtFinalizeErrorINSTANCE = FfiConverterOptionalSequencePsbtFinalizeError{}

func (c FfiConverterOptionalSequencePsbtFinalizeError) Lift(rb RustBufferI) *[]*PsbtFinalizeError {
	return LiftFromRustBuffer[*[]*PsbtFinalizeError](c, rb)
}

func (_ FfiConverterOptionalSequencePsbtFinalizeError) Read(reader io.Reader) *[]*PsbtFinalizeError {
	if readInt8(reader) == 0 {
		return nil
	}
	temp := FfiConverterSequencePsbtFinalizeErrorINSTANCE.Read(reader)
	return &temp
}

func (c FfiConverterOptionalSequencePsbtFinalizeError) Lower(value *[]*PsbtFinalizeError) C.RustBuffer {
	return LowerIntoRustBuffer[*[]*PsbtFinalizeError](c, value)
}

func (_ FfiConverterOptionalSequencePsbtFinalizeError) Write(writer io.Writer, value *[]*PsbtFinalizeError) {
	if value == nil {
		writeInt8(writer, 0)
	} else {
		writeInt8(writer, 1)
		FfiConverterSequencePsbtFinalizeErrorINSTANCE.Write(writer, *value)
	}
}

type FfiDestroyerOptionalSequencePsbtFinalizeError struct{}

func (_ FfiDestroyerOptionalSequencePsbtFinalizeError) Destroy(value *[]*PsbtFinalizeError) {
	if value != nil {
		FfiDestroyerSequencePsbtFinalizeError{}.Destroy(*value)
	}
}

type FfiConverterSequenceUint8 struct{}

var FfiConverterSequenceUint8INSTANCE = FfiConverterSequenceUint8{}

func (c FfiConverterSequenceUint8) Lift(rb RustBufferI) []uint8 {
	return LiftFromRustBuffer[[]uint8](c, rb)
}

func (c FfiConverterSequenceUint8) Read(reader io.Reader) []uint8 {
	length := readInt32(reader)
	if length == 0 {
		return nil
	}
	result := make([]uint8, 0, length)
	for i := int32(0); i < length; i++ {
		result = append(result, FfiConverterUint8INSTANCE.Read(reader))
	}
	return result
}

func (c FfiConverterSequenceUint8) Lower(value []uint8) C.RustBuffer {
	return LowerIntoRustBuffer[[]uint8](c, value)
}

func (c FfiConverterSequenceUint8) Write(writer io.Writer, value []uint8) {
	if len(value) > math.MaxInt32 {
		panic("[]uint8 is too large to fit into Int32")
	}

	writeInt32(writer, int32(len(value)))
	for _, item := range value {
		FfiConverterUint8INSTANCE.Write(writer, item)
	}
}

type FfiDestroyerSequenceUint8 struct{}

func (FfiDestroyerSequenceUint8) Destroy(sequence []uint8) {
	for _, value := range sequence {
		FfiDestroyerUint8{}.Destroy(value)
	}
}

type FfiConverterSequenceUint64 struct{}

var FfiConverterSequenceUint64INSTANCE = FfiConverterSequenceUint64{}

func (c FfiConverterSequenceUint64) Lift(rb RustBufferI) []uint64 {
	return LiftFromRustBuffer[[]uint64](c, rb)
}

func (c FfiConverterSequenceUint64) Read(reader io.Reader) []uint64 {
	length := readInt32(reader)
	if length == 0 {
		return nil
	}
	result := make([]uint64, 0, length)
	for i := int32(0); i < length; i++ {
		result = append(result, FfiConverterUint64INSTANCE.Read(reader))
	}
	return result
}

func (c FfiConverterSequenceUint64) Lower(value []uint64) C.RustBuffer {
	return LowerIntoRustBuffer[[]uint64](c, value)
}

func (c FfiConverterSequenceUint64) Write(writer io.Writer, value []uint64) {
	if len(value) > math.MaxInt32 {
		panic("[]uint64 is too large to fit into Int32")
	}

	writeInt32(writer, int32(len(value)))
	for _, item := range value {
		FfiConverterUint64INSTANCE.Write(writer, item)
	}
}

type FfiDestroyerSequenceUint64 struct{}

func (FfiDestroyerSequenceUint64) Destroy(sequence []uint64) {
	for _, value := range sequence {
		FfiDestroyerUint64{}.Destroy(value)
	}
}

type FfiConverterSequenceDescriptor struct{}

var FfiConverterSequenceDescriptorINSTANCE = FfiConverterSequenceDescriptor{}

func (c FfiConverterSequenceDescriptor) Lift(rb RustBufferI) []*Descriptor {
	return LiftFromRustBuffer[[]*Descriptor](c, rb)
}

func (c FfiConverterSequenceDescriptor) Read(reader io.Reader) []*Descriptor {
	length := readInt32(reader)
	if length == 0 {
		return nil
	}
	result := make([]*Descriptor, 0, length)
	for i := int32(0); i < length; i++ {
		result = append(result, FfiConverterDescriptorINSTANCE.Read(reader))
	}
	return result
}

func (c FfiConverterSequenceDescriptor) Lower(value []*Descriptor) C.RustBuffer {
	return LowerIntoRustBuffer[[]*Descriptor](c, value)
}

func (c FfiConverterSequenceDescriptor) Write(writer io.Writer, value []*Descriptor) {
	if len(value) > math.MaxInt32 {
		panic("[]*Descriptor is too large to fit into Int32")
	}

	writeInt32(writer, int32(len(value)))
	for _, item := range value {
		FfiConverterDescriptorINSTANCE.Write(writer, item)
	}
}

type FfiDestroyerSequenceDescriptor struct{}

func (FfiDestroyerSequenceDescriptor) Destroy(sequence []*Descriptor) {
	for _, value := range sequence {
		FfiDestroyerDescriptor{}.Destroy(value)
	}
}

type FfiConverterSequencePolicy struct{}

var FfiConverterSequencePolicyINSTANCE = FfiConverterSequencePolicy{}

func (c FfiConverterSequencePolicy) Lift(rb RustBufferI) []*Policy {
	return LiftFromRustBuffer[[]*Policy](c, rb)
}

func (c FfiConverterSequencePolicy) Read(reader io.Reader) []*Policy {
	length := readInt32(reader)
	if length == 0 {
		return nil
	}
	result := make([]*Policy, 0, length)
	for i := int32(0); i < length; i++ {
		result = append(result, FfiConverterPolicyINSTANCE.Read(reader))
	}
	return result
}

func (c FfiConverterSequencePolicy) Lower(value []*Policy) C.RustBuffer {
	return LowerIntoRustBuffer[[]*Policy](c, value)
}

func (c FfiConverterSequencePolicy) Write(writer io.Writer, value []*Policy) {
	if len(value) > math.MaxInt32 {
		panic("[]*Policy is too large to fit into Int32")
	}

	writeInt32(writer, int32(len(value)))
	for _, item := range value {
		FfiConverterPolicyINSTANCE.Write(writer, item)
	}
}

type FfiDestroyerSequencePolicy struct{}

func (FfiDestroyerSequencePolicy) Destroy(sequence []*Policy) {
	for _, value := range sequence {
		FfiDestroyerPolicy{}.Destroy(value)
	}
}

type FfiConverterSequenceAddressInfo struct{}

var FfiConverterSequenceAddressInfoINSTANCE = FfiConverterSequenceAddressInfo{}

func (c FfiConverterSequenceAddressInfo) Lift(rb RustBufferI) []AddressInfo {
	return LiftFromRustBuffer[[]AddressInfo](c, rb)
}

func (c FfiConverterSequenceAddressInfo) Read(reader io.Reader) []AddressInfo {
	length := readInt32(reader)
	if length == 0 {
		return nil
	}
	result := make([]AddressInfo, 0, length)
	for i := int32(0); i < length; i++ {
		result = append(result, FfiConverterAddressInfoINSTANCE.Read(reader))
	}
	return result
}

func (c FfiConverterSequenceAddressInfo) Lower(value []AddressInfo) C.RustBuffer {
	return LowerIntoRustBuffer[[]AddressInfo](c, value)
}

func (c FfiConverterSequenceAddressInfo) Write(writer io.Writer, value []AddressInfo) {
	if len(value) > math.MaxInt32 {
		panic("[]AddressInfo is too large to fit into Int32")
	}

	writeInt32(writer, int32(len(value)))
	for _, item := range value {
		FfiConverterAddressInfoINSTANCE.Write(writer, item)
	}
}

type FfiDestroyerSequenceAddressInfo struct{}

func (FfiDestroyerSequenceAddressInfo) Destroy(sequence []AddressInfo) {
	for _, value := range sequence {
		FfiDestroyerAddressInfo{}.Destroy(value)
	}
}

type FfiConverterSequenceCanonicalTx struct{}

var FfiConverterSequenceCanonicalTxINSTANCE = FfiConverterSequenceCanonicalTx{}

func (c FfiConverterSequenceCanonicalTx) Lift(rb RustBufferI) []CanonicalTx {
	return LiftFromRustBuffer[[]CanonicalTx](c, rb)
}

func (c FfiConverterSequenceCanonicalTx) Read(reader io.Reader) []CanonicalTx {
	length := readInt32(reader)
	if length == 0 {
		return nil
	}
	result := make([]CanonicalTx, 0, length)
	for i := int32(0); i < length; i++ {
		result = append(result, FfiConverterCanonicalTxINSTANCE.Read(reader))
	}
	return result
}

func (c FfiConverterSequenceCanonicalTx) Lower(value []CanonicalTx) C.RustBuffer {
	return LowerIntoRustBuffer[[]CanonicalTx](c, value)
}

func (c FfiConverterSequenceCanonicalTx) Write(writer io.Writer, value []CanonicalTx) {
	if len(value) > math.MaxInt32 {
		panic("[]CanonicalTx is too large to fit into Int32")
	}

	writeInt32(writer, int32(len(value)))
	for _, item := range value {
		FfiConverterCanonicalTxINSTANCE.Write(writer, item)
	}
}

type FfiDestroyerSequenceCanonicalTx struct{}

func (FfiDestroyerSequenceCanonicalTx) Destroy(sequence []CanonicalTx) {
	for _, value := range sequence {
		FfiDestroyerCanonicalTx{}.Destroy(value)
	}
}

type FfiConverterSequenceCondition struct{}

var FfiConverterSequenceConditionINSTANCE = FfiConverterSequenceCondition{}

func (c FfiConverterSequenceCondition) Lift(rb RustBufferI) []Condition {
	return LiftFromRustBuffer[[]Condition](c, rb)
}

func (c FfiConverterSequenceCondition) Read(reader io.Reader) []Condition {
	length := readInt32(reader)
	if length == 0 {
		return nil
	}
	result := make([]Condition, 0, length)
	for i := int32(0); i < length; i++ {
		result = append(result, FfiConverterConditionINSTANCE.Read(reader))
	}
	return result
}

func (c FfiConverterSequenceCondition) Lower(value []Condition) C.RustBuffer {
	return LowerIntoRustBuffer[[]Condition](c, value)
}

func (c FfiConverterSequenceCondition) Write(writer io.Writer, value []Condition) {
	if len(value) > math.MaxInt32 {
		panic("[]Condition is too large to fit into Int32")
	}

	writeInt32(writer, int32(len(value)))
	for _, item := range value {
		FfiConverterConditionINSTANCE.Write(writer, item)
	}
}

type FfiDestroyerSequenceCondition struct{}

func (FfiDestroyerSequenceCondition) Destroy(sequence []Condition) {
	for _, value := range sequence {
		FfiDestroyerCondition{}.Destroy(value)
	}
}

type FfiConverterSequenceLocalOutput struct{}

var FfiConverterSequenceLocalOutputINSTANCE = FfiConverterSequenceLocalOutput{}

func (c FfiConverterSequenceLocalOutput) Lift(rb RustBufferI) []LocalOutput {
	return LiftFromRustBuffer[[]LocalOutput](c, rb)
}

func (c FfiConverterSequenceLocalOutput) Read(reader io.Reader) []LocalOutput {
	length := readInt32(reader)
	if length == 0 {
		return nil
	}
	result := make([]LocalOutput, 0, length)
	for i := int32(0); i < length; i++ {
		result = append(result, FfiConverterLocalOutputINSTANCE.Read(reader))
	}
	return result
}

func (c FfiConverterSequenceLocalOutput) Lower(value []LocalOutput) C.RustBuffer {
	return LowerIntoRustBuffer[[]LocalOutput](c, value)
}

func (c FfiConverterSequenceLocalOutput) Write(writer io.Writer, value []LocalOutput) {
	if len(value) > math.MaxInt32 {
		panic("[]LocalOutput is too large to fit into Int32")
	}

	writeInt32(writer, int32(len(value)))
	for _, item := range value {
		FfiConverterLocalOutputINSTANCE.Write(writer, item)
	}
}

type FfiDestroyerSequenceLocalOutput struct{}

func (FfiDestroyerSequenceLocalOutput) Destroy(sequence []LocalOutput) {
	for _, value := range sequence {
		FfiDestroyerLocalOutput{}.Destroy(value)
	}
}

type FfiConverterSequenceOutPoint struct{}

var FfiConverterSequenceOutPointINSTANCE = FfiConverterSequenceOutPoint{}

func (c FfiConverterSequenceOutPoint) Lift(rb RustBufferI) []OutPoint {
	return LiftFromRustBuffer[[]OutPoint](c, rb)
}

func (c FfiConverterSequenceOutPoint) Read(reader io.Reader) []OutPoint {
	length := readInt32(reader)
	if length == 0 {
		return nil
	}
	result := make([]OutPoint, 0, length)
	for i := int32(0); i < length; i++ {
		result = append(result, FfiConverterOutPointINSTANCE.Read(reader))
	}
	return result
}

func (c FfiConverterSequenceOutPoint) Lower(value []OutPoint) C.RustBuffer {
	return LowerIntoRustBuffer[[]OutPoint](c, value)
}

func (c FfiConverterSequenceOutPoint) Write(writer io.Writer, value []OutPoint) {
	if len(value) > math.MaxInt32 {
		panic("[]OutPoint is too large to fit into Int32")
	}

	writeInt32(writer, int32(len(value)))
	for _, item := range value {
		FfiConverterOutPointINSTANCE.Write(writer, item)
	}
}

type FfiDestroyerSequenceOutPoint struct{}

func (FfiDestroyerSequenceOutPoint) Destroy(sequence []OutPoint) {
	for _, value := range sequence {
		FfiDestroyerOutPoint{}.Destroy(value)
	}
}

type FfiConverterSequenceScriptAmount struct{}

var FfiConverterSequenceScriptAmountINSTANCE = FfiConverterSequenceScriptAmount{}

func (c FfiConverterSequenceScriptAmount) Lift(rb RustBufferI) []ScriptAmount {
	return LiftFromRustBuffer[[]ScriptAmount](c, rb)
}

func (c FfiConverterSequenceScriptAmount) Read(reader io.Reader) []ScriptAmount {
	length := readInt32(reader)
	if length == 0 {
		return nil
	}
	result := make([]ScriptAmount, 0, length)
	for i := int32(0); i < length; i++ {
		result = append(result, FfiConverterScriptAmountINSTANCE.Read(reader))
	}
	return result
}

func (c FfiConverterSequenceScriptAmount) Lower(value []ScriptAmount) C.RustBuffer {
	return LowerIntoRustBuffer[[]ScriptAmount](c, value)
}

func (c FfiConverterSequenceScriptAmount) Write(writer io.Writer, value []ScriptAmount) {
	if len(value) > math.MaxInt32 {
		panic("[]ScriptAmount is too large to fit into Int32")
	}

	writeInt32(writer, int32(len(value)))
	for _, item := range value {
		FfiConverterScriptAmountINSTANCE.Write(writer, item)
	}
}

type FfiDestroyerSequenceScriptAmount struct{}

func (FfiDestroyerSequenceScriptAmount) Destroy(sequence []ScriptAmount) {
	for _, value := range sequence {
		FfiDestroyerScriptAmount{}.Destroy(value)
	}
}

type FfiConverterSequenceTxIn struct{}

var FfiConverterSequenceTxInINSTANCE = FfiConverterSequenceTxIn{}

func (c FfiConverterSequenceTxIn) Lift(rb RustBufferI) []TxIn {
	return LiftFromRustBuffer[[]TxIn](c, rb)
}

func (c FfiConverterSequenceTxIn) Read(reader io.Reader) []TxIn {
	length := readInt32(reader)
	if length == 0 {
		return nil
	}
	result := make([]TxIn, 0, length)
	for i := int32(0); i < length; i++ {
		result = append(result, FfiConverterTxInINSTANCE.Read(reader))
	}
	return result
}

func (c FfiConverterSequenceTxIn) Lower(value []TxIn) C.RustBuffer {
	return LowerIntoRustBuffer[[]TxIn](c, value)
}

func (c FfiConverterSequenceTxIn) Write(writer io.Writer, value []TxIn) {
	if len(value) > math.MaxInt32 {
		panic("[]TxIn is too large to fit into Int32")
	}

	writeInt32(writer, int32(len(value)))
	for _, item := range value {
		FfiConverterTxInINSTANCE.Write(writer, item)
	}
}

type FfiDestroyerSequenceTxIn struct{}

func (FfiDestroyerSequenceTxIn) Destroy(sequence []TxIn) {
	for _, value := range sequence {
		FfiDestroyerTxIn{}.Destroy(value)
	}
}

type FfiConverterSequenceTxOut struct{}

var FfiConverterSequenceTxOutINSTANCE = FfiConverterSequenceTxOut{}

func (c FfiConverterSequenceTxOut) Lift(rb RustBufferI) []TxOut {
	return LiftFromRustBuffer[[]TxOut](c, rb)
}

func (c FfiConverterSequenceTxOut) Read(reader io.Reader) []TxOut {
	length := readInt32(reader)
	if length == 0 {
		return nil
	}
	result := make([]TxOut, 0, length)
	for i := int32(0); i < length; i++ {
		result = append(result, FfiConverterTxOutINSTANCE.Read(reader))
	}
	return result
}

func (c FfiConverterSequenceTxOut) Lower(value []TxOut) C.RustBuffer {
	return LowerIntoRustBuffer[[]TxOut](c, value)
}

func (c FfiConverterSequenceTxOut) Write(writer io.Writer, value []TxOut) {
	if len(value) > math.MaxInt32 {
		panic("[]TxOut is too large to fit into Int32")
	}

	writeInt32(writer, int32(len(value)))
	for _, item := range value {
		FfiConverterTxOutINSTANCE.Write(writer, item)
	}
}

type FfiDestroyerSequenceTxOut struct{}

func (FfiDestroyerSequenceTxOut) Destroy(sequence []TxOut) {
	for _, value := range sequence {
		FfiDestroyerTxOut{}.Destroy(value)
	}
}

type FfiConverterSequencePkOrF struct{}

var FfiConverterSequencePkOrFINSTANCE = FfiConverterSequencePkOrF{}

func (c FfiConverterSequencePkOrF) Lift(rb RustBufferI) []PkOrF {
	return LiftFromRustBuffer[[]PkOrF](c, rb)
}

func (c FfiConverterSequencePkOrF) Read(reader io.Reader) []PkOrF {
	length := readInt32(reader)
	if length == 0 {
		return nil
	}
	result := make([]PkOrF, 0, length)
	for i := int32(0); i < length; i++ {
		result = append(result, FfiConverterPkOrFINSTANCE.Read(reader))
	}
	return result
}

func (c FfiConverterSequencePkOrF) Lower(value []PkOrF) C.RustBuffer {
	return LowerIntoRustBuffer[[]PkOrF](c, value)
}

func (c FfiConverterSequencePkOrF) Write(writer io.Writer, value []PkOrF) {
	if len(value) > math.MaxInt32 {
		panic("[]PkOrF is too large to fit into Int32")
	}

	writeInt32(writer, int32(len(value)))
	for _, item := range value {
		FfiConverterPkOrFINSTANCE.Write(writer, item)
	}
}

type FfiDestroyerSequencePkOrF struct{}

func (FfiDestroyerSequencePkOrF) Destroy(sequence []PkOrF) {
	for _, value := range sequence {
		FfiDestroyerPkOrF{}.Destroy(value)
	}
}

type FfiConverterSequencePsbtFinalizeError struct{}

var FfiConverterSequencePsbtFinalizeErrorINSTANCE = FfiConverterSequencePsbtFinalizeError{}

func (c FfiConverterSequencePsbtFinalizeError) Lift(rb RustBufferI) []*PsbtFinalizeError {
	return LiftFromRustBuffer[[]*PsbtFinalizeError](c, rb)
}

func (c FfiConverterSequencePsbtFinalizeError) Read(reader io.Reader) []*PsbtFinalizeError {
	length := readInt32(reader)
	if length == 0 {
		return nil
	}
	result := make([]*PsbtFinalizeError, 0, length)
	for i := int32(0); i < length; i++ {
		result = append(result, FfiConverterPsbtFinalizeErrorINSTANCE.Read(reader))
	}
	return result
}

func (c FfiConverterSequencePsbtFinalizeError) Lower(value []*PsbtFinalizeError) C.RustBuffer {
	return LowerIntoRustBuffer[[]*PsbtFinalizeError](c, value)
}

func (c FfiConverterSequencePsbtFinalizeError) Write(writer io.Writer, value []*PsbtFinalizeError) {
	if len(value) > math.MaxInt32 {
		panic("[]*PsbtFinalizeError is too large to fit into Int32")
	}

	writeInt32(writer, int32(len(value)))
	for _, item := range value {
		FfiConverterPsbtFinalizeErrorINSTANCE.Write(writer, item)
	}
}

type FfiDestroyerSequencePsbtFinalizeError struct{}

func (FfiDestroyerSequencePsbtFinalizeError) Destroy(sequence []*PsbtFinalizeError) {
	for _, value := range sequence {
		FfiDestroyerPsbtFinalizeError{}.Destroy(value)
	}
}

type FfiConverterSequenceSequenceUint8 struct{}

var FfiConverterSequenceSequenceUint8INSTANCE = FfiConverterSequenceSequenceUint8{}

func (c FfiConverterSequenceSequenceUint8) Lift(rb RustBufferI) [][]uint8 {
	return LiftFromRustBuffer[[][]uint8](c, rb)
}

func (c FfiConverterSequenceSequenceUint8) Read(reader io.Reader) [][]uint8 {
	length := readInt32(reader)
	if length == 0 {
		return nil
	}
	result := make([][]uint8, 0, length)
	for i := int32(0); i < length; i++ {
		result = append(result, FfiConverterSequenceUint8INSTANCE.Read(reader))
	}
	return result
}

func (c FfiConverterSequenceSequenceUint8) Lower(value [][]uint8) C.RustBuffer {
	return LowerIntoRustBuffer[[][]uint8](c, value)
}

func (c FfiConverterSequenceSequenceUint8) Write(writer io.Writer, value [][]uint8) {
	if len(value) > math.MaxInt32 {
		panic("[][]uint8 is too large to fit into Int32")
	}

	writeInt32(writer, int32(len(value)))
	for _, item := range value {
		FfiConverterSequenceUint8INSTANCE.Write(writer, item)
	}
}

type FfiDestroyerSequenceSequenceUint8 struct{}

func (FfiDestroyerSequenceSequenceUint8) Destroy(sequence [][]uint8) {
	for _, value := range sequence {
		FfiDestroyerSequenceUint8{}.Destroy(value)
	}
}

type FfiConverterMapUint16Float64 struct{}

var FfiConverterMapUint16Float64INSTANCE = FfiConverterMapUint16Float64{}

func (c FfiConverterMapUint16Float64) Lift(rb RustBufferI) map[uint16]float64 {
	return LiftFromRustBuffer[map[uint16]float64](c, rb)
}

func (_ FfiConverterMapUint16Float64) Read(reader io.Reader) map[uint16]float64 {
	result := make(map[uint16]float64)
	length := readInt32(reader)
	for i := int32(0); i < length; i++ {
		key := FfiConverterUint16INSTANCE.Read(reader)
		value := FfiConverterFloat64INSTANCE.Read(reader)
		result[key] = value
	}
	return result
}

func (c FfiConverterMapUint16Float64) Lower(value map[uint16]float64) C.RustBuffer {
	return LowerIntoRustBuffer[map[uint16]float64](c, value)
}

func (_ FfiConverterMapUint16Float64) Write(writer io.Writer, mapValue map[uint16]float64) {
	if len(mapValue) > math.MaxInt32 {
		panic("map[uint16]float64 is too large to fit into Int32")
	}

	writeInt32(writer, int32(len(mapValue)))
	for key, value := range mapValue {
		FfiConverterUint16INSTANCE.Write(writer, key)
		FfiConverterFloat64INSTANCE.Write(writer, value)
	}
}

type FfiDestroyerMapUint16Float64 struct{}

func (_ FfiDestroyerMapUint16Float64) Destroy(mapValue map[uint16]float64) {
	for key, value := range mapValue {
		FfiDestroyerUint16{}.Destroy(key)
		FfiDestroyerFloat64{}.Destroy(value)
	}
}

type FfiConverterMapUint32SequenceCondition struct{}

var FfiConverterMapUint32SequenceConditionINSTANCE = FfiConverterMapUint32SequenceCondition{}

func (c FfiConverterMapUint32SequenceCondition) Lift(rb RustBufferI) map[uint32][]Condition {
	return LiftFromRustBuffer[map[uint32][]Condition](c, rb)
}

func (_ FfiConverterMapUint32SequenceCondition) Read(reader io.Reader) map[uint32][]Condition {
	result := make(map[uint32][]Condition)
	length := readInt32(reader)
	for i := int32(0); i < length; i++ {
		key := FfiConverterUint32INSTANCE.Read(reader)
		value := FfiConverterSequenceConditionINSTANCE.Read(reader)
		result[key] = value
	}
	return result
}

func (c FfiConverterMapUint32SequenceCondition) Lower(value map[uint32][]Condition) C.RustBuffer {
	return LowerIntoRustBuffer[map[uint32][]Condition](c, value)
}

func (_ FfiConverterMapUint32SequenceCondition) Write(writer io.Writer, mapValue map[uint32][]Condition) {
	if len(mapValue) > math.MaxInt32 {
		panic("map[uint32][]Condition is too large to fit into Int32")
	}

	writeInt32(writer, int32(len(mapValue)))
	for key, value := range mapValue {
		FfiConverterUint32INSTANCE.Write(writer, key)
		FfiConverterSequenceConditionINSTANCE.Write(writer, value)
	}
}

type FfiDestroyerMapUint32SequenceCondition struct{}

func (_ FfiDestroyerMapUint32SequenceCondition) Destroy(mapValue map[uint32][]Condition) {
	for key, value := range mapValue {
		FfiDestroyerUint32{}.Destroy(key)
		FfiDestroyerSequenceCondition{}.Destroy(value)
	}
}

type FfiConverterMapStringSequenceUint64 struct{}

var FfiConverterMapStringSequenceUint64INSTANCE = FfiConverterMapStringSequenceUint64{}

func (c FfiConverterMapStringSequenceUint64) Lift(rb RustBufferI) map[string][]uint64 {
	return LiftFromRustBuffer[map[string][]uint64](c, rb)
}

func (_ FfiConverterMapStringSequenceUint64) Read(reader io.Reader) map[string][]uint64 {
	result := make(map[string][]uint64)
	length := readInt32(reader)
	for i := int32(0); i < length; i++ {
		key := FfiConverterStringINSTANCE.Read(reader)
		value := FfiConverterSequenceUint64INSTANCE.Read(reader)
		result[key] = value
	}
	return result
}

func (c FfiConverterMapStringSequenceUint64) Lower(value map[string][]uint64) C.RustBuffer {
	return LowerIntoRustBuffer[map[string][]uint64](c, value)
}

func (_ FfiConverterMapStringSequenceUint64) Write(writer io.Writer, mapValue map[string][]uint64) {
	if len(mapValue) > math.MaxInt32 {
		panic("map[string][]uint64 is too large to fit into Int32")
	}

	writeInt32(writer, int32(len(mapValue)))
	for key, value := range mapValue {
		FfiConverterStringINSTANCE.Write(writer, key)
		FfiConverterSequenceUint64INSTANCE.Write(writer, value)
	}
}

type FfiDestroyerMapStringSequenceUint64 struct{}

func (_ FfiDestroyerMapStringSequenceUint64) Destroy(mapValue map[string][]uint64) {
	for key, value := range mapValue {
		FfiDestroyerString{}.Destroy(key)
		FfiDestroyerSequenceUint64{}.Destroy(value)
	}
}

type FfiConverterMapStringSequenceCondition struct{}

var FfiConverterMapStringSequenceConditionINSTANCE = FfiConverterMapStringSequenceCondition{}

func (c FfiConverterMapStringSequenceCondition) Lift(rb RustBufferI) map[string][]Condition {
	return LiftFromRustBuffer[map[string][]Condition](c, rb)
}

func (_ FfiConverterMapStringSequenceCondition) Read(reader io.Reader) map[string][]Condition {
	result := make(map[string][]Condition)
	length := readInt32(reader)
	for i := int32(0); i < length; i++ {
		key := FfiConverterStringINSTANCE.Read(reader)
		value := FfiConverterSequenceConditionINSTANCE.Read(reader)
		result[key] = value
	}
	return result
}

func (c FfiConverterMapStringSequenceCondition) Lower(value map[string][]Condition) C.RustBuffer {
	return LowerIntoRustBuffer[map[string][]Condition](c, value)
}

func (_ FfiConverterMapStringSequenceCondition) Write(writer io.Writer, mapValue map[string][]Condition) {
	if len(mapValue) > math.MaxInt32 {
		panic("map[string][]Condition is too large to fit into Int32")
	}

	writeInt32(writer, int32(len(mapValue)))
	for key, value := range mapValue {
		FfiConverterStringINSTANCE.Write(writer, key)
		FfiConverterSequenceConditionINSTANCE.Write(writer, value)
	}
}

type FfiDestroyerMapStringSequenceCondition struct{}

func (_ FfiDestroyerMapStringSequenceCondition) Destroy(mapValue map[string][]Condition) {
	for key, value := range mapValue {
		FfiDestroyerString{}.Destroy(key)
		FfiDestroyerSequenceCondition{}.Destroy(value)
	}
}
