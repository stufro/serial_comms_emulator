# Pathfinder: "The Martian" Comms Terminal over Virtual Serial

An interactive serial communications emulator themed after **The Martian** (Ares III / Pathfinder / JPL Deep Space Network), running a custom binary framing protocol with real-time live byte streaming over virtual `socat` PTY devices.

---

## Frame Structure & Wire Legend

```
+-------------------+-------------------+---------------------+-------------------------+--------------------+
| Sync Word (2B)    | Sequence ID (4B)  | Payload Length (2B) | Payload (N Bytes)       | CRC32 (4B)         |
| 0xAA 0x55         | uint32 (BigEndian)| uint16 (BigEndian)  | Telemetry / Raw bytes   | uint32 (BigEndian) |
+-------------------+-------------------+---------------------+-------------------------+--------------------+
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

To watch the bytes travel across the "wire" in real-time, change the `socat` command to include an intermediary dump into a file:
```bash
socat PTY,link=/tmp/ttyV0,raw,echo=0 - | tee tmp/wire.bin | socat - PTY,link=/tmp/ttyV1,raw,echo=0
```

Then, in a separate terminal, run:
```bash
tail -f tmp/wire.bin | hexdump -C
```

Example output:
```
00000000  aa 55 00 00 00 01 00 34  5b 57 41 54 4e 45 59 20  |.U.....4[WATNEY |
00000010  28 41 52 45 53 20 33 20  48 41 42 29 20 7c 20 53  |(ARES 3 HAB) | S|
00000020  4f 4c 20 31 33 35 20 7c  20 31 30 3a 31 34 3a 35  |OL 135 | 10:14:5|
00000030  34 5d 20 48 69 20 74 68  65 72 65 2e d2 1b 56 b3  |4] Hi there...V.|
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
