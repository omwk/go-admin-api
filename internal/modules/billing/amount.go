package billing

import (
	"math"

	"github.com/omwk/go-admin-api/internal/kernel/errs"
)

// ChargeAmount calculates an exact positive linear charge without overflow.
func ChargeAmount(price, units int64) (int64, error) {
	if price <= 0 || units <= 0 || price > math.MaxInt64/units {
		return 0, Invalid()
	}
	return price * units, nil
}

// Invalid rejects monetary syntax and unsupported currencies without echoing values.
func Invalid() error { return errs.New(10001, 400, "计费参数无效") }

// Unavailable fails closed when a durable financial fact cannot be verified.
func Unavailable() error { return errs.New(10002, 503, "服务暂不可用") }

// Missing distinguishes an absent wallet from a failed dependency.
func Missing() error { return errs.New(90101, 404, "钱包不存在") }

// Insufficient rejects admission without changing frozen funds.
func Insufficient() error { return errs.New(90102, 402, "余额不足") }

// Inactive rejects new reservations on an administratively frozen wallet.
func Inactive() error { return errs.New(90103, 403, "钱包已停用") }

// FrozenInsufficient reports an inconsistent reservation without borrowing another call's funds.
func FrozenInsufficient() error { return errs.New(90104, 409, "冻结金额不足") }

// Mismatch rejects reuse of an operation identity with different content.
func Mismatch() error { return errs.New(10006, 409, "幂等键与请求不一致") }

// TerminalConflict prevents a reservation from acquiring two terminal outcomes.
func TerminalConflict() error { return errs.New(10004, 409, "收费已终结") }

// InvalidCode deliberately conflates missing, expired and already used credentials.
func InvalidCode() error { return errs.New(90301, 400, "兑换码无效或已使用") }
