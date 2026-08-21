# Pathfinder: "The Martian" Comms Terminal over Virtual Serial

An interactive serial communications emulator themed after **The Martian** (Ares III / Pathfinder / JPL Deep Space Network), running a custom binary framing protocol with real-time live byte streaming over virtual `socat` PTY devices.

---

## Frame Structure & Wire Legend

```
+-------------------+-------------------+---------------------+-------------------------+--------------------+
| Sync Word (2B)    | Sequence ID (4B)  | Payload Length (2B) | Payload (N Bytes)       | CRC32 (4B)         |
| 0xAA 0x55         | uint32 (BigEndian)| uint16 (BigEndian)  | Telemetry / Raw bytes   | uint32 (BigEndian) |
+-------------------+-------------------+---------------------+-------------------------+--------------------+
  [Cyan]              [Yellow]            [Magenta]             [Green]                   [Orange]
```

- **Sync Word (`0xAA 0x55`)**: Delimits frame start across unstructured streams.
- **Sequence ID (`uint32`)**: Monotonic frame sequence number.
- **Payload Length (`uint16`)**: Big-endian payload byte count.
- **Payload**: Message string (e.g. `[WATNEY | SOL 135 | 14:22:01] I'M ALIVE`).
- **CRC32 Checksum (`uint32`)**: IEEE 802.3 checksum verified at the receiver.

---

## Quick Start

### 1. Create the Virtual Serial Wire (`socat`)
In a dedicated terminal:
```bash
socat PTY,link=/tmp/ttyV0,raw,echo=0 PTY,link=/tmp/ttyV1,raw,echo=0
```

### 2. Launch JPL Ground Station (Receiver)
In Terminal 1:
```bash
go run ./cmd/receiver -port /tmp/ttyV1
```

### 3. Launch Pathfinder Terminal (Sender)
In Terminal 2:
```bash
go run ./cmd/sender -port /tmp/ttyV0 -operator "WATNEY (HAB)" -sol 135 -byte-delay 35ms
```

---

## Live Raw Frame Build-Up

When you transmit a message, you will see each hex byte rendered in real time as it hits the wire, color-coded by protocol field:

```text
[WATNEY (ARES 3 HAB) | SOL 135 | SEQ #001] > BRING ME HOME
  📡 Wire Stream: [AA 55 00 00 00 01 00 34 5B 57 41 54 ... 2B 4A 91 C2]
  ↳ [TX CONFIRMED] 64 bytes wire | Seq #1 | Checksum: 0x00000004
```

---

## In-Terminal Commands (Sender)

While in the Pathfinder sender prompt:
- Type your message and hit **`ENTER`** to stream the binary frame.
- `/delay <duration>`: Adjust byte trickle speed on the fly (e.g. `/delay 50ms`, `/delay 10ms`, or `/delay 0s`).
- `/sol <n>`: Update mission Sol day (e.g. `/sol 142`).
- `/operator <name>`: Switch operator callsign (e.g. `/operator JPL UPLINK`).
- `/clear`: Clear screen and reprint station banner.
- `/help`: Display available in-terminal commands.
- `/exit`: Terminate comms link.
