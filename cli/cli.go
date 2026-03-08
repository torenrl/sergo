package cli

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"gnist/sergo/serial"

	"go.bug.st/serial/enumerator"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

var (
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("15")).
			Background(lipgloss.Color("62")).
			Padding(0, 1)

	statusStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("15")).
			Background(lipgloss.Color("236")).
			Padding(0, 1)

	selectedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("10")).Bold(true)
	cursorStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("11"))
	dimStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	hintStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("14")).Italic(true)
	inputLabel    = lipgloss.NewStyle().Foreground(lipgloss.Color("12")).Bold(true)
	borderStyle   = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "240", Dark: "250"})
)

var commonBaudRates = []int{9600, 19200, 38400, 57600, 115200, 230400, 460800, 921600}

type phase int

const (
	phasePort phase = iota
	phaseBaud
	phaseTerminal
)

type serialDataMsg string
type serialErrMsg struct{ err error }
type selectionTickMsg struct{}
type selectionPortsMsg struct {
	ports []portOption
}
type reconnectTickMsg struct{}
type reconnectResultMsg struct {
	port *serial.Port
	err  error
}

var trailingNumRe = regexp.MustCompile(`^(.*?)(\d+)$`)
var ioregUSBProductRe = regexp.MustCompile(`"USB Product Name"\s*=\s*"([^"]+)"`)
var ioregVendorIDRe = regexp.MustCompile(`"idVendor"\s*=\s*(\d+)`)
var ioregProductIDRe = regexp.MustCompile(`"idProduct"\s*=\s*(\d+)`)
var ioregSerialRe = regexp.MustCompile(`"(?:USB Serial Number|Serial Number|kUSBSerialNumberString)"\s*=\s*"([^"]+)"`)
var ioregTTYDeviceRe = regexp.MustCompile(`"IOTTYDevice"\s*=\s*"([^"]+)"`)
var ioregDialinRe = regexp.MustCompile(`"IODialinDevice"\s*=\s*"([^"]+)"`)
var ioregCalloutRe = regexp.MustCompile(`"IOCalloutDevice"\s*=\s*"([^"]+)"`)

type termLine struct {
	plain  []rune
	styles []string
}

type portOption struct {
	raw     string
	device  string
	osName  string
	product string
	vidpid  string
	serial  string
	portNum int
	isUSB   bool
	display string
	sortNum int
	sortHas bool
}

type usbMeta struct {
	product string
	vidpid  string
	serial  string
}

type model struct {
	phase  phase
	ports  []portOption
	bauds  []int
	cursor int

	portName string
	baudRate int

	port       *serial.Port
	vp         viewport.Model
	input      textinput.Model
	output     string
	termLines  []termLine
	termCol    int
	escSeen    bool
	csiMode    bool
	csiBuf     []byte
	oscMode    bool
	oscEscSeen bool
	currentSGR string
	history    []string
	histIdx    int
	serialCh   chan string
	directMode bool
	prefix     bool // true after Ctrl+X, waiting for second key
	debugMode  bool
	debugLast  string
	pendingTxBS    int
	autoReconnect bool
	reconnectWait bool
	reconnectErr  string

	width    int
	height   int
	quitting bool
	connErr  error
}

func initialModel(ports []portOption) model {
	return model{
		phase:         phasePort,
		ports:         ports,
		bauds:         commonBaudRates,
		autoReconnect: true,
	}
}

func waitForSerial(ch chan string) tea.Cmd {
	return func() tea.Msg {
		data, ok := <-ch
		if !ok {
			return serialErrMsg{err: fmt.Errorf("connection closed")}
		}
		return serialDataMsg(data)
	}
}

func (m model) Init() tea.Cmd {
	return scheduleSelectionRefresh()
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case selectionTickMsg:
		if m.phase == phasePort {
			return m, tea.Batch(refreshPortsCmd(), scheduleSelectionRefresh())
		}
		return m, scheduleSelectionRefresh()
	case selectionPortsMsg:
		if m.phase == phasePort {
			m.applyRefreshedPorts(msg.ports)
		}
		return m, nil
	case reconnectTickMsg:
		if m.phase == phaseTerminal && m.reconnectWait && m.autoReconnect {
			return m, tryReconnectCmd(m.portName, m.baudRate)
		}
		return m, nil
	case reconnectResultMsg:
		if m.phase != phaseTerminal || !m.reconnectWait {
			if msg.port != nil {
				_ = msg.port.Close()
			}
			return m, nil
		}
		if msg.err != nil {
			m.reconnectErr = msg.err.Error()
			return m, scheduleReconnectTick()
		}
		m.port = msg.port
		m.reconnectWait = false
		m.reconnectErr = ""
		// Start fresh after reconnect.
		m.output = ""
		m.termLines = []termLine{{}}
		m.termCol = 0
		m.escSeen = false
		m.csiMode = false
		m.csiBuf = nil
		m.oscMode = false
		m.oscEscSeen = false
		m.currentSGR = "\x1b[0m"
		m.vp.SetContent("")
		m.vp.GotoTop()
		m.serialCh = make(chan string, 64)
		go func(port *serial.Port, ch chan string) {
			buf := make([]byte, 1024)
			for {
				n, err := port.Read(buf)
				if err != nil {
					close(ch)
					return
				}
				if n > 0 {
					ch <- string(buf[:n])
				}
			}
		}(m.port, m.serialCh)
		m.appendSerialData("[reconnected]\n")
		m.vp.SetContent(m.output)
		m.vp.GotoBottom()
		return m, waitForSerial(m.serialCh)
	}

	if msg, ok := msg.(tea.WindowSizeMsg); ok {
		m.width = msg.Width
		m.height = msg.Height
		if m.phase == phaseTerminal {
			m.resizeTerminal()
		}
	}

	switch m.phase {
	case phasePort, phaseBaud:
		return m.updateSelection(msg)
	case phaseTerminal:
		return m.updateTerminal(msg)
	}
	return m, nil
}

// --- selection phases ---

func (m model) updateSelection(msg tea.Msg) (tea.Model, tea.Cmd) {
	km, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch km.String() {
	case "ctrl+c", "q":
		m.quitting = true
		return m, tea.Quit
	case "d":
		if m.phase == phasePort {
			m.debugMode = !m.debugMode
		}
		return m, nil
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < m.listLen()-1 {
			m.cursor++
		}
	case "enter":
		if m.phase == phasePort {
			m.portName = m.ports[m.cursor].raw
			m.phase = phaseBaud
			m.cursor = defaultBaudIdx(m.bauds)
		} else {
			m.baudRate = m.bauds[m.cursor]
			return m.openTerminal()
		}
	}
	return m, nil
}

func (m model) listLen() int {
	if m.phase == phasePort {
		return len(m.ports)
	}
	return len(m.bauds)
}

func scheduleSelectionRefresh() tea.Cmd {
	return tea.Tick(1500*time.Millisecond, func(time.Time) tea.Msg {
		return selectionTickMsg{}
	})
}

func refreshPortsCmd() tea.Cmd {
	return func() tea.Msg {
		ports, err := serial.ListPorts()
		if err != nil {
			return selectionPortsMsg{ports: nil}
		}
		return selectionPortsMsg{ports: buildPortOptions(ports)}
	}
}

func scheduleReconnectTick() tea.Cmd {
	return tea.Tick(1*time.Second, func(time.Time) tea.Msg {
		return reconnectTickMsg{}
	})
}

func tryReconnectCmd(portName string, baud int) tea.Cmd {
	return func() tea.Msg {
		cfg := serial.DefaultConfig()
		cfg.BaudRate = baud
		p, err := serial.Open(portName, cfg)
		if err != nil {
			return reconnectResultMsg{err: err}
		}
		return reconnectResultMsg{port: p}
	}
}

func (m *model) applyRefreshedPorts(newPorts []portOption) {
	if len(newPorts) == 0 {
		return
	}
	currentRaw := ""
	if m.cursor >= 0 && m.cursor < len(m.ports) {
		currentRaw = m.ports[m.cursor].raw
	}
	m.ports = newPorts
	if currentRaw == "" {
		if m.cursor >= len(m.ports) {
			m.cursor = 0
		}
		return
	}
	for i, p := range m.ports {
		if p.raw == currentRaw {
			m.cursor = i
			return
		}
	}
	if m.cursor >= len(m.ports) {
		m.cursor = len(m.ports) - 1
		if m.cursor < 0 {
			m.cursor = 0
		}
	}
}

func buildPortOptions(ports []string) []portOption {
	detailByName := map[string]*enumerator.PortDetails{}
	if details, err := enumerator.GetDetailedPortsList(); err == nil {
		for _, d := range details {
			if d != nil {
				detailByName[d.Name] = d
			}
		}
	}
	darwinByTTY := map[string]usbMeta{}
	darwinByPath := map[string]usbMeta{}
	if runtime.GOOS == "darwin" {
		darwinByTTY, darwinByPath = lookupDarwinTTYProductMap()
	}

	// First pass: de-duplicate equivalent ports (e.g. /dev/tty.* and /dev/cu.*)
	// and gather parsing metadata.
	dedup := map[string]portOption{}
	for _, raw := range ports {
		detail := detailByName[raw]
		base := filepath.Base(raw)
		norm := strings.TrimPrefix(base, "cu.")
		norm = strings.TrimPrefix(norm, "tty.")
		device, n, has := splitDeviceAndPort(norm)
		isUSB := isUSBPort(norm)
		if detail != nil {
			isUSB = detail.IsUSB
			if strings.TrimSpace(detail.Product) != "" {
				device = strings.TrimSpace(detail.Product)
			}
		}
		if device == "" || strings.HasPrefix(strings.ToLower(device), "usbmodem") {
			if meta, ok := darwinByTTY[norm]; ok && meta.product != "" {
				device = meta.product
			} else if meta, ok := darwinByPath[raw]; ok && meta.product != "" {
				device = meta.product
			}
		}

		opt := portOption{
			raw:     raw,
			device:  device,
			osName:  raw,
			isUSB:   isUSB,
			sortNum: n,
			sortHas: has,
		}
		if detail != nil {
			opt.product = strings.TrimSpace(detail.Product)
			opt.serial = strings.TrimSpace(detail.SerialNumber)
			if detail.VID != "" || detail.PID != "" {
				opt.vidpid = strings.ToUpper(detail.VID) + ":" + strings.ToUpper(detail.PID)
			}
		}
		if opt.product == "" {
			if meta, ok := darwinByTTY[norm]; ok && meta.product != "" {
				opt.product = meta.product
			} else if meta, ok := darwinByPath[raw]; ok && meta.product != "" {
				opt.product = meta.product
			}
		}
		if opt.vidpid == "" {
			if meta, ok := darwinByTTY[norm]; ok && meta.vidpid != "" {
				opt.vidpid = meta.vidpid
			} else if meta, ok := darwinByPath[raw]; ok && meta.vidpid != "" {
				opt.vidpid = meta.vidpid
			}
		}
		if opt.serial == "" {
			if meta, ok := darwinByTTY[norm]; ok && meta.serial != "" {
				opt.serial = meta.serial
			} else if meta, ok := darwinByPath[raw]; ok && meta.serial != "" {
				opt.serial = meta.serial
			}
		}
		dedupKey := strings.ToLower(norm)
		if prev, ok := dedup[dedupKey]; ok {
			if preferPortPath(opt.raw, prev.raw) {
				dedup[dedupKey] = opt
			}
		} else {
			dedup[dedupKey] = opt
		}
	}

	// Second pass: collect options.
	options := make([]portOption, 0, len(dedup))
	for _, opt := range dedup {
		options = append(options, opt)
	}

	// USB devices first, then name, then natural numeric suffix for stable order.
	sort.Slice(options, func(i, j int) bool {
		a, b := options[i], options[j]
		if a.isUSB != b.isUSB {
			return a.isUSB
		}
		ad, bd := strings.ToLower(a.device), strings.ToLower(b.device)
		if ad != bd {
			return ad < bd
		}
		if a.sortHas && b.sortHas && a.sortNum != b.sortNum {
			return a.sortNum < b.sortNum
		}
		if a.sortHas != b.sortHas {
			return a.sortHas
		}
		return strings.ToLower(a.osName) < strings.ToLower(b.osName)
	})

	// Sequential per-device port index starting from 0.
	countByDevice := map[string]int{}
	for i := range options {
		key := strings.ToLower(options[i].device)
		countByDevice[key]++
	}
	idxByDevice := map[string]int{}
	for i := range options {
		key := strings.ToLower(options[i].device)
		options[i].portNum = idxByDevice[key]
		idxByDevice[key]++
		if countByDevice[key] <= 1 {
			options[i].display = options[i].device
		} else {
			options[i].display = fmt.Sprintf("%s %d", options[i].device, options[i].portNum)
		}
	}
	return options
}

func splitDeviceAndPort(base string) (string, int, bool) {
	m := trailingNumRe.FindStringSubmatch(base)
	if len(m) != 3 {
		return base, 0, false
	}
	n, err := strconv.Atoi(m[2])
	if err != nil {
		return base, 0, false
	}
	name := strings.TrimRight(m[1], "-_ .")
	if name == "" {
		name = base
	}
	return name, n, true
}

func preferPortPath(candidate, current string) bool {
	score := func(path string) int {
		switch {
		case strings.Contains(path, "/tty."):
			return 0
		case strings.Contains(path, "/cu."):
			return 1
		default:
			return 2
		}
	}
	cs, xs := score(candidate), score(current)
	if cs != xs {
		return cs < xs
	}
	return strings.ToLower(candidate) < strings.ToLower(current)
}

func isUSBPort(base string) bool {
	b := strings.ToLower(base)
	return strings.Contains(b, "usb") ||
		strings.Contains(b, "acm") ||
		strings.Contains(b, "modem") ||
		strings.Contains(b, "serial")
}

func lookupDarwinTTYProductMap() (map[string]usbMeta, map[string]usbMeta) {
	byTTY := map[string]usbMeta{}
	byPath := map[string]usbMeta{}

	out, err := exec.Command("ioreg", "-p", "IOService", "-w0", "-l", "-c", "IOUSBHostInterface").Output()
	if err != nil {
		return byTTY, byPath
	}

	lines := strings.Split(string(out), "\n")
	current := usbMeta{}
	for _, line := range lines {
		if strings.Contains(line, "+-o ") {
			// New node; keep most recently discovered metadata flowing downward.
		}
		if m := ioregUSBProductRe.FindStringSubmatch(line); len(m) == 2 {
			current.product = strings.TrimSpace(m[1])
			continue
		}
		if m := ioregVendorIDRe.FindStringSubmatch(line); len(m) == 2 {
			if n, err := strconv.Atoi(m[1]); err == nil {
				if current.vidpid == "" {
					current.vidpid = fmt.Sprintf("%04X", n)
				} else {
					parts := strings.Split(current.vidpid, ":")
					if len(parts) == 2 {
						current.vidpid = fmt.Sprintf("%04X:%s", n, parts[1])
					}
				}
			}
			continue
		}
		if m := ioregProductIDRe.FindStringSubmatch(line); len(m) == 2 {
			if n, err := strconv.Atoi(m[1]); err == nil {
				pid := fmt.Sprintf("%04X", n)
				if current.vidpid == "" {
					current.vidpid = "0000:" + pid
				} else {
					parts := strings.Split(current.vidpid, ":")
					if len(parts) == 2 {
						current.vidpid = parts[0] + ":" + pid
					}
				}
			}
			continue
		}
		if m := ioregSerialRe.FindStringSubmatch(line); len(m) == 2 {
			current.serial = strings.TrimSpace(m[1])
			continue
		}
		if m := ioregTTYDeviceRe.FindStringSubmatch(line); len(m) == 2 {
			byTTY[m[1]] = current
			continue
		}
		if m := ioregDialinRe.FindStringSubmatch(line); len(m) == 2 {
			byPath[m[1]] = current
			continue
		}
		if m := ioregCalloutRe.FindStringSubmatch(line); len(m) == 2 {
			byPath[m[1]] = current
			continue
		}
	}
	return byTTY, byPath
}

func defaultBaudIdx(bauds []int) int {
	for i, b := range bauds {
		if b == 115200 {
			return i
		}
	}
	return 0
}

// --- terminal phase ---

func (m model) openTerminal() (model, tea.Cmd) {
	cfg := serial.DefaultConfig()
	cfg.BaudRate = m.baudRate

	port, err := serial.Open(m.portName, cfg)
	if err != nil {
		m.connErr = err
		m.quitting = true
		return m, tea.Quit
	}

	m.port = port
	m.phase = phaseTerminal
	m.output = ""
	m.termLines = []termLine{{}}
	m.termCol = 0
	m.escSeen = false
	m.csiMode = false
	m.csiBuf = nil
	m.oscMode = false
	m.oscEscSeen = false
	m.currentSGR = "\x1b[0m"
	m.serialCh = make(chan string, 64)

	ti := textinput.New()
	ti.Placeholder = "type command…"
	ti.Focus()
	ti.CharLimit = 512
	m.input = ti

	m.vp = viewport.New(m.width, 1)
	m.resizeTerminal()

	go func() {
		buf := make([]byte, 1024)
		for {
			n, err := port.Read(buf)
			if err != nil {
				close(m.serialCh)
				return
			}
			if n > 0 {
				m.serialCh <- string(buf[:n])
			}
		}
	}()

	return m, tea.Batch(textinput.Blink, waitForSerial(m.serialCh))
}

func (m *model) resizeTerminal() {
	// layout: title(1) + viewport + separator(1) + input/prefix(1) + hints(1)
	vpH := m.height - 4
	if vpH < 1 {
		vpH = 1
	}
	m.vp.Width = m.width
	m.vp.Height = vpH
	m.input.Width = m.width - 4
	m.vp.SetContent(m.output)
}

func (m model) updateTerminal(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case serialDataMsg:
		m.appendSerialData(string(msg))
		m.vp.SetContent(m.output)
		m.vp.GotoBottom()
		cmds = append(cmds, waitForSerial(m.serialCh))

	case serialErrMsg:
		m.appendSerialData("\n[disconnected]\n")
		m.vp.SetContent(m.output)
		m.vp.GotoBottom()
		if m.autoReconnect {
			if m.port != nil {
				_ = m.port.Close()
			}
			m.port = nil
			m.serialCh = nil
			m.reconnectWait = true
			m.reconnectErr = ""
			m.appendSerialData("[waiting on device...]\n")
			m.vp.SetContent(m.output)
			m.vp.GotoBottom()
			return m, scheduleReconnectTick()
		}
		return m.disconnectToSelection()

	case tea.KeyMsg:
		key := msg.String()

		if m.prefix {
			return m.handleChord(key)
		}

		if key == "ctrl+x" {
			m.prefix = true
			return m, nil
		}

		if m.directMode {
			return m.handleDirectKey(msg)
		}

		return m.handleLineKey(msg)
	}

	var tiCmd tea.Cmd
	m.input, tiCmd = m.input.Update(msg)
	cmds = append(cmds, tiCmd)

	return m, tea.Batch(cmds...)
}

func (m *model) appendSerialData(chunk string) {
	if len(m.termLines) == 0 {
		m.termLines = []termLine{{}}
		m.termCol = 0
	}

	for i := 0; i < len(chunk); i++ {
		b := chunk[i]
		if m.debugMode {
			switch b {
			case 0x08:
				m.debugLast = "RX: BS (0x08)"
			case 0x7f:
				m.debugLast = "RX: DEL (0x7f)"
			case '\r':
				m.debugLast = "RX: CR (0x0d)"
			case '\n':
				m.debugLast = "RX: LF (0x0a)"
			}
		}

		// Consume OSC (ESC ] ... BEL/ST) payload fully.
		if m.oscMode {
			if b == 0x07 { // BEL terminator
				m.oscMode = false
				m.oscEscSeen = false
				continue
			}
			if m.oscEscSeen && b == '\\' { // ST terminator
				m.oscMode = false
				m.oscEscSeen = false
				continue
			}
			m.oscEscSeen = (b == 0x1b)
			continue
		}

		// Consume CSI (ESC [ ... final-byte).
		if m.csiMode {
			m.csiBuf = append(m.csiBuf, b)
			if b >= 0x40 && b <= 0x7e {
				m.handleCSI(b, m.csiBuf)
				m.csiMode = false
				m.csiBuf = nil
			}
			continue
		}

		// ESC introducer.
		if m.escSeen {
			m.escSeen = false
			switch b {
			case '[':
				m.csiMode = true
				m.csiBuf = []byte{0x1b, '['}
			case ']':
				m.oscMode = true
				m.oscEscSeen = false
			default:
				// Ignore other ESC commands to prevent terminal control side-effects.
			}
			continue
		}

		if b == 0x1b {
			m.escSeen = true
			continue
		}

		switch b {
		case '\r':
			m.termCol = 0
		case '\n':
			m.termLines = append(m.termLines, termLine{})
			m.termCol = 0
		case '\b':
			if m.termCol > 0 {
				m.termCol--
			}
			if m.pendingTxBS > 0 {
				m.pendingTxBS--
			}
		case 0x7f:
			if m.termCol > 0 {
				m.termCol--
			}
			if m.pendingTxBS > 0 {
				m.pendingTxBS--
			}
		case '\t':
			spaces := 4 - (m.termCol % 4)
			for range spaces {
				m.appendRuneToLine(' ')
			}
		default:
			// Skip remaining C0 controls except printable bytes.
			if b < 0x20 || b == 0x7f {
				continue
			}
			m.appendRuneToLine(rune(b))
		}
	}

	m.output = renderTermLines(m.termLines)
}

func (m *model) handleCSI(final byte, seq []byte) {
	// seq format is ESC [ <params> <final>
	if len(seq) < 3 {
		return
	}

	params := ""
	if len(seq) > 3 {
		params = string(seq[2 : len(seq)-1])
	}

	switch final {
	case 'm':
		// Keep SGR color/style sequences.
		m.currentSGR = string(seq)
	case 'D':
		// Cursor backward (left)
		n := parseCSIInt(params, 1)
		m.termCol -= n
		if m.termCol < 0 {
			m.termCol = 0
		}
		// Zephyr shell often emits backspace as CSI 1 D only.
		// If we just transmitted BS, synthesize one delete-at-cursor.
		if n == 1 && m.pendingTxBS > 0 {
			m.deleteCharsAtCursor(1)
			m.pendingTxBS--
			if m.debugMode {
				m.debugLast = "RX: CSI 1 D + sync DEL1"
			}
		}
	case 'C':
		// Cursor forward (right)
		n := parseCSIInt(params, 1)
		m.termCol += n
	case 'K':
		// Erase in line
		m.eraseInLine(parseCSIInt(params, 0))
	case 'P':
		// Delete chars at cursor (shift left)
		m.deleteCharsAtCursor(parseCSIInt(params, 1))
		if m.pendingTxBS > 0 {
			m.pendingTxBS--
		}
	}
	if m.debugMode {
		switch final {
		case 'D', 'C', 'K', 'P':
			m.debugLast = fmt.Sprintf("RX: CSI %s %c", params, final)
		}
	}
}

func parseCSIInt(params string, def int) int {
	if params == "" {
		return def
	}
	parts := strings.Split(params, ";")
	n, err := strconv.Atoi(parts[0])
	if err != nil || n <= 0 {
		return def
	}
	return n
}

func (m *model) appendRuneToLine(r rune) {
	last := len(m.termLines) - 1
	line := m.termLines[last]
	if m.termCol < len(line.plain) {
		line.plain[m.termCol] = r
		line.styles[m.termCol] = m.currentSGR
	} else {
		for len(line.plain) < m.termCol {
			line.plain = append(line.plain, ' ')
			line.styles = append(line.styles, "\x1b[0m")
		}
		line.plain = append(line.plain, r)
		line.styles = append(line.styles, m.currentSGR)
	}
	m.termLines[last] = line
	m.termCol++
}

func (m *model) erasePreviousCell() {
	if m.termCol <= 0 || len(m.termLines) == 0 {
		return
	}
	last := len(m.termLines) - 1
	line := m.termLines[last]
	pos := m.termCol - 1
	if pos >= len(line.plain) {
		// Cursor is past rendered content; just move left.
		m.termCol--
		return
	}

	line.plain = append(line.plain[:pos], line.plain[pos+1:]...)
	if pos < len(line.styles) {
		line.styles = append(line.styles[:pos], line.styles[pos+1:]...)
	}
	m.termLines[last] = line
	m.termCol--
}

func (m *model) eraseInLine(mode int) {
	if len(m.termLines) == 0 {
		return
	}
	last := len(m.termLines) - 1
	line := m.termLines[last]
	if m.termCol < 0 {
		m.termCol = 0
	}

	switch mode {
	case 1:
		// Start to cursor (inclusive): blank, don't shift.
		end := m.termCol + 1
		if end > len(line.plain) {
			end = len(line.plain)
		}
		for i := 0; i < end; i++ {
			line.plain[i] = ' '
			if i < len(line.styles) {
				line.styles[i] = m.currentSGR
			}
		}
	case 2:
		// Entire line: blank, cursor stays.
		for i := range line.plain {
			line.plain[i] = ' '
			if i < len(line.styles) {
				line.styles[i] = m.currentSGR
			}
		}
	default:
		// Cursor to end: blank, don't shift.
		start := m.termCol
		if start < 0 {
			start = 0
		}
		for i := start; i < len(line.plain); i++ {
			line.plain[i] = ' '
			if i < len(line.styles) {
				line.styles[i] = m.currentSGR
			}
		}
	}

	m.termLines[last] = line
}

func (m *model) deleteCharsAtCursor(n int) {
	if n <= 0 || len(m.termLines) == 0 {
		return
	}
	last := len(m.termLines) - 1
	line := m.termLines[last]
	if m.termCol < 0 {
		m.termCol = 0
	}
	if m.termCol >= len(line.plain) {
		return
	}

	end := m.termCol + n
	if end > len(line.plain) {
		end = len(line.plain)
	}
	line.plain = append(line.plain[:m.termCol], line.plain[end:]...)
	if m.termCol < len(line.styles) {
		styleEnd := end
		if styleEnd > len(line.styles) {
			styleEnd = len(line.styles)
		}
		line.styles = append(line.styles[:m.termCol], line.styles[styleEnd:]...)
	}
	m.termLines[last] = line
}

func renderTermLines(lines []termLine) string {
	var out strings.Builder
	for i, line := range lines {
		var active string
		for idx, r := range line.plain {
			style := "\x1b[0m"
			if idx < len(line.styles) && line.styles[idx] != "" {
				style = line.styles[idx]
			}
			if style != active {
				out.WriteString(style)
				active = style
			}
			out.WriteRune(r)
		}
		out.WriteString("\x1b[0m")
		if i < len(lines)-1 {
			out.WriteByte('\n')
		}
	}
	return out.String()
}

func (m model) handleChord(key string) (tea.Model, tea.Cmd) {
	// Exit command mode by default; specific keys can keep it active.
	m.prefix = false

	switch key {
	case "esc", "escape":
		return m, nil
	case "q":
		if m.port != nil {
			m.port.Close()
		}
		m.quitting = true
		return m, tea.Quit
	case "t":
		m.directMode = !m.directMode
		if m.directMode {
			m.input.Blur()
		} else {
			m.input.Focus()
		}
		return m, nil
	case "c":
		return m.disconnectToSelection()
	case "r":
		m.autoReconnect = !m.autoReconnect
		if m.autoReconnect {
			m.debugLast = "auto reconnect: ON"
		} else {
			m.debugLast = "auto reconnect: OFF"
		}
		return m, nil
	case "f":
		m.vp.GotoBottom()
		return m, nil
	case "d":
		m.debugMode = !m.debugMode
		if m.debugMode {
			m.debugLast = "debug ON (backspace/control traces)"
		} else {
			m.debugLast = "debug OFF"
		}
		return m, nil
	case "up":
		m.prefix = true
		m.vp.ScrollUp(1)
		return m, nil
	case "down":
		m.prefix = true
		m.vp.ScrollDown(1)
		return m, nil
	}
	return m, nil
}

func (m model) disconnectToSelection() (tea.Model, tea.Cmd) {
	if m.port != nil {
		_ = m.port.Close()
	}
	m.port = nil
	m.phase = phasePort
	m.directMode = false
	m.prefix = false
	m.output = ""
	m.termLines = nil
	m.termCol = 0
	m.escSeen = false
	m.csiMode = false
	m.csiBuf = nil
	m.oscMode = false
	m.oscEscSeen = false
	m.currentSGR = "\x1b[0m"
	m.serialCh = nil
	m.reconnectWait = false
	m.reconnectErr = ""

	return m, refreshPortsCmd()
}

var ansiKeyMap = map[string][]byte{
	"up":    {0x1b, '[', 'A'},
	"down":  {0x1b, '[', 'B'},
	"right": {0x1b, '[', 'C'},
	"left":  {0x1b, '[', 'D'},
	"home":  {0x1b, '[', 'H'},
	"end":   {0x1b, '[', 'F'},
	"tab":   {'\t'},
	"enter": {'\r'},
	"backspace": {0x08},
	"delete":    {0x1b, '[', '3', '~'},
	"escape":    {0x1b},
}

func (m model) handleDirectKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.port == nil {
		return m, nil
	}

	key := msg.String()

	if key == "pgup" || key == "pgdown" {
		var vpCmd tea.Cmd
		m.vp, vpCmd = m.vp.Update(msg)
		return m, vpCmd
	}
	if key == "backspace" || key == "backspace2" || key == "deletebackward" || key == "ctrl+h" {
		m.port.Write([]byte{0x08})
		m.pendingTxBS++
		if m.debugMode {
			m.debugLast = "TX: BS (0x08)"
		}
		return m, nil
	}

	if seq, ok := ansiKeyMap[key]; ok {
		m.port.Write(seq)
		return m, nil
	}

	if strings.HasPrefix(key, "ctrl+") && len(key) == 6 {
		ch := key[5] - 'a' + 1
		m.port.Write([]byte{ch})
		return m, nil
	}

	if runes := msg.Runes; len(runes) > 0 {
		m.port.Write([]byte(string(runes)))
		return m, nil
	}

	return m, nil
}

func (m model) handleLineKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "up":
		if len(m.history) > 0 && m.histIdx > 0 {
			m.histIdx--
			m.input.SetValue(m.history[m.histIdx])
			m.input.CursorEnd()
		}
		return m, nil

	case "down":
		if m.histIdx < len(m.history)-1 {
			m.histIdx++
			m.input.SetValue(m.history[m.histIdx])
			m.input.CursorEnd()
		} else if m.histIdx >= len(m.history)-1 {
			m.histIdx = len(m.history)
			m.input.SetValue("")
		}
		return m, nil

	case "pgup", "pgdown":
		var vpCmd tea.Cmd
		m.vp, vpCmd = m.vp.Update(msg)
		return m, vpCmd

	case "enter":
		text := m.input.Value()
		if text != "" {
			m.history = append(m.history, text)
		}
		m.histIdx = len(m.history)
		m.input.SetValue("")
		if m.port != nil {
			m.port.Write([]byte(text + "\r\n"))
		}
		return m, nil
	}

	var tiCmd tea.Cmd
	m.input, tiCmd = m.input.Update(msg)
	return m, tiCmd
}

// --- views ---

func (m model) View() string {
	if m.quitting {
		return ""
	}
	switch m.phase {
	case phasePort, phaseBaud:
		return m.viewSelection()
	case phaseTerminal:
		return m.viewTerminal()
	}
	return ""
}

func (m model) viewSelection() string {
	var b strings.Builder

	var title string
	if m.phase == phasePort {
		title = "Select serial port"
	} else {
		title = "Select baud rate"
	}

	bar := titleStyle.Width(m.width).Render(title)
	b.WriteString(bar)
	b.WriteString("\n\n")

	if m.phase == phasePort {
		for i, p := range m.ports {
			if i == m.cursor {
				b.WriteString(cursorStyle.Render("  ▸ "))
				b.WriteString(selectedStyle.Render(p.display))
				b.WriteString("  ")
				b.WriteString(dimStyle.Render(p.osName))
			} else {
				b.WriteString("    ")
				b.WriteString(p.display)
				b.WriteString("  ")
				b.WriteString(dimStyle.Render(p.osName))
			}
			b.WriteString("\n")
			if m.debugMode {
				dbg := fmt.Sprintf("      dbg product=%q vidpid=%q serial=%q usb=%t raw=%q", p.product, p.vidpid, p.serial, p.isUSB, p.raw)
				b.WriteString(dimStyle.Render(dbg))
				b.WriteString("\n")
			}
		}
	} else {
		for i, br := range m.bauds {
			item := fmt.Sprintf("%d", br)
			if i == m.cursor {
				b.WriteString(cursorStyle.Render("  ▸ "))
				b.WriteString(selectedStyle.Render(item))
			} else {
				b.WriteString(dimStyle.Render("    " + item))
			}
			b.WriteString("\n")
		}
	}
	b.WriteString("\n")

	b.WriteString("\n")
	if m.phase == phaseBaud {
		b.WriteString(dimStyle.Render("  port: "+m.portName) + "\n")
	}
	if m.phase == phasePort {
		b.WriteString(hintStyle.Render("  ↑/↓ navigate  enter select  d debug  q quit"))
	} else {
		b.WriteString(hintStyle.Render("  ↑/↓ navigate  enter select  q quit"))
	}

	return b.String()
}

var (
	modeLineStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("15")).
			Background(lipgloss.Color("33")).
			Padding(0, 1)

	modeDirectStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("15")).
			Background(lipgloss.Color("196")).
			Padding(0, 1)

	reconOnStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("15")).
			Background(lipgloss.Color("28")).
			Padding(0, 1)

	reconOffStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("15")).
			Background(lipgloss.Color("88")).
			Padding(0, 1)
)

func (m model) viewTerminal() string {
	modeTag := modeLineStyle.Render("LINE")
	if m.directMode {
		modeTag = modeDirectStyle.Render("DIRECT")
	}
	reconTag := reconOffStyle.Render("RECON OFF")
	if m.autoReconnect {
		reconTag = reconOnStyle.Render("RECON ON")
	}
	info := fmt.Sprintf(" sergo — %s @ %d ", m.portName, m.baudRate)
	titleWidth := m.width - lipgloss.Width(modeTag) - lipgloss.Width(reconTag)
	if titleWidth < 0 {
		titleWidth = 0
	}
	bar := titleStyle.Width(titleWidth).Render(info)
	topLine := bar + modeTag + reconTag

	sep := borderStyle.Render(strings.Repeat("─", m.width))

	inputLine := ""
	if m.prefix {
		rc := "reconnect:on"
		if !m.autoReconnect {
			rc = "reconnect:off"
		}
		inputLine = inputLabel.Render("C-x ") + dimStyle.Render("q quit  c disconnect  t toggle mode  r "+rc+"  ↑/↓ scroll  esc exit")
	} else if m.directMode {
		inputLine = dimStyle.Render("  keys -> serial")
	} else {
		inputLine = inputLabel.Render("❯ ") + m.input.View()
	}

	var hints string
	if m.prefix {
		hints = hintStyle.Render("C-x mode: q quit  c disconnect  t toggle mode  r reconnect on/off  f end  d debug  ↑/↓ scroll  esc exit")
	} else if m.directMode {
		hints = hintStyle.Render("C-x commands  pgup/pgdn scroll")
	} else {
		hints = hintStyle.Render("C-x commands  pgup/pgdn scroll  ↑/↓ history")
	}
	if m.reconnectWait {
		if m.reconnectErr != "" {
			hints = hintStyle.Render("waiting on device... C-x c to return to selection  (" + m.reconnectErr + ")")
		} else {
			hints = hintStyle.Render("waiting on device... C-x c to return to selection")
		}
	} else if m.debugMode && m.debugLast != "" {
		hints = hintStyle.Render("DBG " + m.debugLast)
	}
	pad := m.width - lipgloss.Width(hints)
	if pad < 0 {
		pad = 0
	}
	hintLine := strings.Repeat(" ", pad) + hints

	// Keep frame height stable so the top bar remains pinned.
	return strings.Join([]string{
		topLine,
		m.vp.View(),
		sep,
		inputLine,
		hintLine,
	}, "\n")
}

// --- entry point ---

func Run() error {
	ports, err := serial.ListPorts()
	if err != nil {
		return err
	}
	if len(ports) == 0 {
		return fmt.Errorf("no serial ports found")
	}

	p := tea.NewProgram(initialModel(buildPortOptions(ports)), tea.WithAltScreen())
	result, err := p.Run()
	if err != nil {
		return fmt.Errorf("TUI error: %w", err)
	}

	m := result.(model)
	if m.connErr != nil {
		return m.connErr
	}
	return nil
}
