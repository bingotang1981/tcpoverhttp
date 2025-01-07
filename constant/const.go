package constant

const (
	Establish = "establish"
	Forward   = "forward"
	Require   = "require"
	Read      = "read"
	Write     = "write"
	Goodbye   = "goodbye"
)

const BuffSize = 32 * 1024
const KEYPADDING = "012345678901234567890123456789ab"
const HEART_BEAT_INTERVAL = 90

const NORMAL = byte(0)
const TIMEOUT = byte(1)
const ERROR = byte(2)
const NO_ID = byte(3)
const NO_ITEM_ID = byte(4)
const DUP_ITEM_ID = byte(5)
const EOF = byte(6)
