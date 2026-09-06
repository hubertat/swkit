# WAGO Modbus TCP – Digital I/O Design Notes

This document summarizes how digital inputs and outputs are addressed and used on a WAGO Modbus TCP coupler (e.g. 750-362) and how the Go driver should interact with it.

---

## 1. General Model

- Hardware modules are arranged physically on the coupler.
- The coupler builds an internal **process image** at startup.
- For **digital I/O**, WAGO exposes a **bit-based Modbus view** that is stable and easy to use.
- The driver should **not rely on register-based digital access**, only on coils/discrete inputs.

---

## 2. Digital Inputs (DI)

### Addressing
- Digital inputs are addressed **sequentially starting from 0**
- Address = **global input channel index**
- Module boundaries do not matter at runtime

Example mapping:

| Module | Channels | DI addresses |
|------|---------|-------------|
| 750-4xx | 8 | 0 – 7 |
| 750-4xx | 2 | 8 – 9 |

Total inputs = **10 bits (0–9)**

### Modbus access
- **Function code:** FC2 – Read Discrete Inputs
- **Read:** start `0`, quantity = number of DI bits

---

## 3. Digital Outputs (DO)

WAGO exposes outputs in **two regions**:

### 3.1 Write-Only Output Area (WO)
- Used to **control outputs**
- Addressed **sequentially from 0**
- These are the addresses you write to

### 3.2 Read/Write Output Image (RW)
- Used to **read back actual output state**
- Starts at **address 512**
- Represents the output process image

### Example mapping

| Logical DO | Write (WO) | Readback (RW) |
|---------|-----------|---------------|
| DO0 | 0 | 512 |
| DO1 | 1 | 513 |
| DO2 | 2 | 514 |
| DO3 | 3 | 515 |

### Modbus access
- **Write outputs**
  - FC5 – Write Single Coil
  - FC15 – Write Multiple Coils
  - Addresses: `0..N-1`
- **Read outputs (recommended)**
  - FC1 – Read Coils
  - Addresses: `512..512+N-1`

⚠️ Do **not** rely on reading WO addresses – always read from RW.

---

## 4. Polling & Command Strategy

### Regular polling
- Inputs: `FC2`, start `0`, qty = DI count
- Output readback: `FC1`, start `512`, qty = DO count

### Command writes
- Single output toggle → `FC5`
- Batch update → `FC15`

Keep one persistent Modbus TCP connection per node.

---

## 5. Unsafe / Sensitive Modbus Areas

### Safe by default
- **Coils (digital I/O)** outside valid ranges return *Illegal Address*
- Writing only to known DO coil ranges is safe

### Potentially dangerous
**Holding registers (FC6 / FC16)** include:
- KBUS reset
- Software reset
- Factory reset
- Watchdog configuration

Examples (do NOT touch unless intentional):
- KBUS reset
- Software reset (magic values)
- Factory settings restore

**Rule:**  
👉 Driver should **never write holding registers** unless explicitly in maintenance/service mode.

---

## 6. Hardware Verification via Modbus (Automation-Friendly)

### 6.1 Verify I/O counts
Useful to detect configuration mismatches.

| Register | Meaning |
|--------|--------|
| `0x1024` | Number of digital output bits |
| `0x1025` | Number of digital input bits |

Example:
- Expect DI = 10
- Expect DO = 4  
Mismatch → hardware/config error

---

### 6.2 Discover module structure & order
Registers:
- `0x2030 .. 0x2033` – module list in physical order

For **digital modules**, returned words encode:
- Input vs output
- Number of channels
- Digital module flag

This allows:
- Detecting module order
- Detecting DI vs DO modules
- Detecting channel widths

⚠️ Exact WAGO part numbers (750-xxx) are **not available** for digital modules via Modbus, only structure.

---

### 6.3 Diagnostics
Optional but useful:

| Register | Purpose |
|--------|--------|
| `0x1020 / 0x1021` | LED error code + argument |
| `0x1050` | Module/channel diagnostics (short circuit, wire break, etc.) |

---

## 7. Recommended Driver Architecture

### Startup
1. Read `0x1024` / `0x1025` → verify DI/DO counts
2. Optionally read `0x2030` → verify module structure/order
3. Fail fast if mismatch

### Runtime
- Poll DI (FC2)
- Poll DO readback (FC1 @ 512)
- Write DO only via FC5/FC15

### Configuration
- Store expected module layout (DI/DO + channel counts)
- Treat WBM “Modbus Mapping” page as the authoritative reference

---

## 8. Key Takeaways

- Digital I/O addressing is **simple, sequential, and stable**
- **Write outputs at 0..N-1**
- **Read outputs at 512..**
- Avoid holding registers unless doing explicit maintenance
- Modbus allows enough introspection to auto-verify hardware at startup

