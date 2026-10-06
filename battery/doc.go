// Package battery implements a battery-pack protection and current derating
// manager.
//
// Each periodic sample carries a timestamp, every cell voltage, every
// temperature point (use InvalidTemperature for failed sensors) and the pack
// current (mA; positive charges, negative discharges). The manager derives a
// consistent Snapshot after every accepted sample:
//
//   - AllowedChargeMA / AllowedDischargeMA: the most restrictive of the voltage
//     table, temperature table and rated current for that direction.
//   - BanCharge / BanDischarge: voltage protection states with confirmation
//     timers and hysteresis-based release.
//   - BalanceRequest: non-latching cell-spread balancing request.
//   - SensorFault: all temperature points invalid; both limits forced to zero.
//   - Latched / LatchCauses: manual-reset faults (overcurrent, voltage delta).
//
// All methods are safe for concurrent use. Reads never observe a partially
// applied sample. See DESIGN.md for the module split and key trade-offs.
package battery
