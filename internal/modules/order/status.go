package order

import "github.com/omwk/go-admin-api/internal/kernel/errs"

// StateChange records only an observed pair, never hypothetical intermediate steps.
type StateChange struct{ Dimension, From, To string }

// StateDecision either accepts all dimensions or preserves the entire aggregate.
type StateDecision struct {
	Changes []StateChange
	Reason  string
}

var stateGraphs = map[string]map[string][]string{
	"lifecycle": {
		"unknown":         {"pending_payment", "paid", "shipped", "completed", "cancelled"},
		"pending_payment": {"paid", "cancelled"}, "paid": {"shipped", "cancelled"},
		"shipped": {"completed"}, "completed": {}, "cancelled": {},
	},
	"fulfillment": {"unknown": {"unshipped", "shipped", "delivered"}, "unshipped": {"shipped"}, "shipped": {"delivered"}, "delivered": {}},
	"payment":     {"unknown": {"unpaid", "paid", "partial_refund", "refunded"}, "unpaid": {"paid"}, "paid": {"partial_refund", "refunded"}, "partial_refund": {"refunded"}, "refunded": {}},
	"after_sales": {"unknown": {"none", "refunding", "partial_refunded", "refunded"}, "none": {"refunding", "partial_refunded", "refunded"}, "refunding": {"none", "partial_refunded", "refunded"}, "partial_refunded": {"refunding", "refunded"}, "refunded": {}},
	"cancel":      {"unknown": {"none", "requested", "rejected", "cancelled"}, "none": {"requested", "cancelled"}, "requested": {"rejected", "cancelled"}, "rejected": {"requested"}, "cancelled": {}},
}

func stateValues(s States) []string {
	return []string{s.Lifecycle, s.Fulfillment, s.Payment, s.AfterSales, s.Cancel}
}
func dimensions() []string {
	return []string{"lifecycle", "fulfillment", "payment", "after_sales", "cancel"}
}

// DecideStates allows snapshot reachability, single-step commands and evidenced correction.
func DecideStates(from, to States, mode ChangeMode, correctionProved bool) (StateDecision, error) {
	var decision StateDecision
	old, next := stateValues(from), stateValues(to)
	for i, dimension := range dimensions() {
		graph := stateGraphs[dimension]
		if _, exists := graph[next[i]]; !exists {
			decision.Reason = "unknown_state"
		}
		if next[i] == old[i] {
			continue
		}
		decision.Changes = append(decision.Changes, StateChange{dimension, old[i], next[i]})
		if mode == ChangeCommand && (dimension == "payment" || !directStep(graph, old[i], next[i])) {
			return decision, errs.New(50301, 400, "订单状态非法流转")
		}
		if next[i] == "unknown" && old[i] != "" && old[i] != "unknown" {
			decision.Reason = "unknown_state"
		}
		if old[i] != "" && !reachable(graph, old[i], next[i], map[string]bool{}) && !correctionProved {
			if decision.Reason == "" {
				decision.Reason = "rollback_requires_evidence"
			}
		}
	}
	if mode == ChangeCorrection && !correctionProved {
		decision.Reason = "correction_requires_evidence"
	}
	return decision, nil
}

func directStep(graph map[string][]string, from, to string) bool {
	for _, candidate := range graph[from] {
		if candidate == to {
			return true
		}
	}
	return false
}

func reachable(graph map[string][]string, from, to string, seen map[string]bool) bool {
	if from == to {
		return true
	}
	if seen[from] {
		return false
	}
	seen[from] = true
	for _, next := range graph[from] {
		if reachable(graph, next, to, seen) {
			return true
		}
	}
	return false
}

// DisplayStatus projects compatible UI states without losing orthogonal facts.
func (s States) DisplayStatus() string {
	if s.Payment == "refunded" && s.AfterSales == "refunded" {
		return "refunded"
	}
	if s.AfterSales == "refunding" {
		return "refunding"
	}
	switch s.Lifecycle {
	case "paid":
		return "pending_shipment"
	case "cancelled":
		return "closed"
	case "pending_payment", "shipped", "completed":
		return s.Lifecycle
	default:
		return "unknown"
	}
}

func consistentStates(s Snapshot) bool {
	m, v := s.Money, s.States
	if v.Payment == "refunded" && (m.PaidAmountCents == 0 || m.RefundedAmountCents != m.PaidAmountCents) {
		return false
	}
	if v.Payment == "partial_refund" && (m.RefundedAmountCents <= 0 || m.RefundedAmountCents >= m.PaidAmountCents) {
		return false
	}
	if v.Payment == "unpaid" && s.PaymentSettled {
		return false
	}
	if v.Payment == "paid" && m.RefundedAmountCents != 0 {
		return false
	}
	if v.AfterSales == "refunded" && v.Payment != "refunded" {
		return false
	}
	if v.AfterSales == "partial_refunded" && v.Payment != "partial_refund" {
		return false
	}
	if v.Lifecycle == "pending_payment" && v.Payment != "unpaid" && v.Payment != "unknown" {
		return false
	}
	if v.Lifecycle == "cancelled" && v.Cancel != "cancelled" {
		return false
	}
	if v.Cancel == "cancelled" && v.Lifecycle != "cancelled" {
		return false
	}
	if (v.Lifecycle == "shipped" || v.Lifecycle == "completed") && v.Fulfillment == "unshipped" {
		return false
	}
	return true
}

func decideAggregate(old Order, s Snapshot) (StateDecision, error) {
	proved := correctionProved(old, s)
	decision, err := DecideStates(old.States, s.States, s.Mode, proved)
	if err != nil {
		return decision, err
	}
	if !consistentStates(s) {
		decision.Reason = "inconsistent_states"
	}
	if (s.Money.PaidAmountCents < old.PaidAmountCents || s.Money.RefundedAmountCents < old.RefundedAmountCents) && !proved {
		decision.Reason = "rollback_requires_evidence"
	}
	if s.Mode == ChangeCommand && (s.Money != old.Money || s.Items != nil || s.Address != nil) {
		return decision, errs.New(50301, 400, "订单状态非法流转")
	}
	return decision, nil
}
