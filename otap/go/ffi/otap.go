package ffi

// #cgo LDFLAGS: -L${SRCDIR}/../../rust/target/release -lotap_integration
// #include <stdint.h>
// #include <stddef.h>
// extern int32_t  otap_modq_init(const char* shm_name, uint32_t ring_depth, uint32_t slot_size);
// extern uint32_t otap_modq_submit(const uint8_t* payload, size_t len);
import "C"
import (
	"fmt"
	"unsafe"
)

// InitMODQ initializes the Rust MODQ queue from Go
func InitMODQ(shmName string, ringDepth, slotSize uint32) error {
	cName := C.CString(shmName)
	defer C.free(unsafe.Pointer(cName))

	ret := C.otap_modq_init(cName, C.uint32_t(ringDepth), C.uint32_t(slotSize))
	if ret != 0 {
		return fmt.Errorf("MODQ init failed")
	}
	return nil
}

// SubmitMODQ submits a payload to Rust MODQ from Go
func SubmitMODQ(payload []byte) (uint32, error) {
	if len(payload) == 0 {
		return 0, fmt.Errorf("empty payload")
	}

	sid := C.otap_modq_submit((*C.uint8_t)(unsafe.Pointer(&payload[0])), C.size_t(len(payload)))
	if sid == 0xFFFFFFFF {
		return 0, fmt.Errorf("submit failed")
	}
	return uint32(sid), nil
}
