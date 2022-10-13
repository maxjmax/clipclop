// The ugly part.
package x

import (
	"fmt"
	"reflect"

	"github.com/BurntSushi/xgb"
	"github.com/BurntSushi/xgb/xfixes"
	"github.com/BurntSushi/xgb/xproto"
	"github.com/maxjmax/clipclop/history"
)

// TODO: remove history.ClipFormat from here, convert from atoms outside of this package.

type incr struct {
	data      []byte // data we are sending
	i         int    // index to write next
	seq       uint16
	selection xproto.Atom
	target    xproto.Atom
	property  xproto.Atom
}

type X struct {
	conn        *xgb.Conn
	window      xproto.Window
	screen      *xproto.ScreenInfo
	atoms       atoms
	incrs       map[xproto.Window]*incr
	maxPropSize int // maximum number of bytes for a property
}

type atoms struct {
	selectionProperty xproto.Atom
	clipboard         xproto.Atom
	targets           xproto.Atom
	incr              xproto.Atom
	png               xproto.Atom
	utf8              xproto.Atom
}

func StartX() (*X, error) {
	conn, err := xgb.NewConn()
	if err != nil {
		return nil, fmt.Errorf("could not create X connection: %w", err)
	}

	if err = xfixes.Init(conn); err != nil {
		return nil, fmt.Errorf("could not init xfixes extension : %w", err)
	}

	setup := xproto.Setup(conn)
	screen := setup.DefaultScreen(conn)

	// From https://www.x.org/releases/current/doc/fixesproto/fixesproto.txt
	// The client must negotiate the version of the extension before executing
	// extension requests.  Behavior of the server is undefined otherwise.
	if _, err = xfixes.QueryVersion(conn, 2, 0).Reply(); err != nil {
		return nil, fmt.Errorf("could not negotiate xfixes version: %w", err)
	}

	atoms := atoms{
		selectionProperty: createAtom(conn, "CLIPCLOP_SEL"),
		clipboard:         createAtom(conn, "CLIPBOARD"),
		targets:           createAtom(conn, "TARGETS"),
		incr:              createAtom(conn, "INCR"),
		png:               createAtom(conn, "image/png"),
		utf8:              createAtom(conn, "UTF8_STRING"),
	}
	if atoms.selectionProperty == xproto.AtomNone ||
		atoms.clipboard == xproto.AtomNone ||
		atoms.targets == xproto.AtomNone ||
		atoms.incr == xproto.AtomNone ||
		atoms.png == xproto.AtomNone ||
		atoms.utf8 == xproto.AtomNone {
		return nil, fmt.Errorf("could not create atom: %v", atoms)
	}

	return &X{
		conn:        conn,
		screen:      screen,
		atoms:       atoms,
		maxPropSize: int(setup.MaximumRequestLength), // quarter of the max size in bytes
		incrs:       make(map[xproto.Window]*incr),
	}, nil
}

func (x *X) CreateEventWindow() error {
	wid, err := xproto.NewWindowId(x.conn)
	if err != nil {
		return fmt.Errorf("could not get window ID: %w", err)
	}

	err = xproto.CreateWindowChecked(
		x.conn, x.screen.RootDepth, wid, x.screen.Root, 0, 0, 1, 1, 0,
		xproto.WindowClassInputOutput, x.screen.RootVisual,
		0, []uint32{},
	).Check()
	if err != nil {
		return fmt.Errorf("could not create event window : %w", err)
	}

	// Request events to it when the selection changes
	var mask uint32 = xfixes.SelectionEventMaskSetSelectionOwner
	if err = xfixes.SelectSelectionInputChecked(x.conn, wid, xproto.AtomPrimary, mask).Check(); err != nil {
		return fmt.Errorf("could not select primary selection events: %w", err)
	}

	if err = xfixes.SelectSelectionInputChecked(x.conn, wid, x.atoms.clipboard, mask).Check(); err != nil {
		return fmt.Errorf("could not select clipboard selection events: %w", err)
	}

	x.window = wid
	return nil
}

func (x *X) IsEventWindow(window xproto.Window) bool {
	return x.window == window
}

func (x *X) NextEvent() (xgb.Event, xgb.Error) {
	return x.conn.WaitForEvent()
}

func (x *X) ConvertSelection(ev xfixes.SelectionNotifyEvent) error {
	if x.IsEventWindow(ev.Owner) {
		return nil
	}

	return xproto.ConvertSelectionChecked(
		x.conn, ev.Window, ev.Selection, x.atoms.targets, x.atoms.targets, ev.SelectionTimestamp).Check()
}

func (x *X) GetSelection(ev xproto.SelectionNotifyEvent) ([]uint8, history.ClipFormat, error) {
	if ev.Property == x.atoms.targets {
		target, err := x.chooseTarget(ev)
		if err != nil {
			return nil, history.NoneFormat, fmt.Errorf("failed to choose target: %w", err)
		}
		err = xproto.ConvertSelectionChecked(x.conn, ev.Requestor, ev.Selection, target, x.atoms.selectionProperty, ev.Time).Check()
		if err != nil {
			return nil, history.NoneFormat, fmt.Errorf("error requesting selection convert to %d, %w", target, err)
		}
	} else {
		reply, err := xproto.GetProperty(x.conn, true, ev.Requestor, x.atoms.selectionProperty, ev.Target, 0, (1<<32)-1).Reply()
		if err != nil {
			return nil, history.NoneFormat, fmt.Errorf("failed to get selection prop: %w", err)
		} else if len(reply.Value) > 0 {
			return reply.Value, x.atomToFormat(ev.Target), nil
		}
	}
	// Empty selection
	return nil, history.NoneFormat, nil
}

func (x *X) SetSelection(ev xproto.SelectionRequestEvent, data *[]uint8, format history.ClipFormat) error {
	replaceProperty := func(typ xproto.Atom, format byte, len uint32, data []byte) error {
		return xproto.ChangePropertyChecked(
			x.conn, xproto.PropModeReplace, ev.Requestor, ev.Property,
			typ, format, len, data,
		).Check()
	}

	var err error
	dataLen := uint32(len(*data))
	if ev.Target == x.atoms.targets {
		err = replaceProperty(xproto.AtomAtom, 32, 2, packInts(uint32(x.formatToAtom(format)), uint32(x.atoms.targets)))
	} else if int(dataLen) < x.maxPropSize {
		// target := x.formatToAtom(currentClip.format)
		err = replaceProperty(ev.Target, 8, dataLen, []byte(*data))
	} else {
		// Need to use INCR
		err = replaceProperty(x.atoms.incr, 32, 1, packInts(dataLen))
		if err != nil {
			return err
		}
		err = x.selectInput(ev.Requestor, xproto.EventMaskPropertyChange)

		x.incrs[ev.Requestor] = &incr{
			data:      []byte(*data),
			i:         0,
			seq:       ev.Sequence,
			target:    ev.Target,
			property:  ev.Property,
			selection: ev.Selection,
		}
	}

	if err != nil {
		return err
	}

	notifyEvent := xproto.SelectionNotifyEvent{
		Sequence:  ev.Sequence,
		Time:      xproto.TimeCurrentTime,
		Requestor: ev.Requestor,
		Selection: ev.Selection,
		Target:    ev.Target, // TARGETS or whatever they requested
		Property:  ev.Property,
	}

	return xproto.SendEventChecked(x.conn, false, ev.Requestor, xproto.EventMaskNoEvent, string(notifyEvent.Bytes())).Check()
}

func (x *X) ContinueSetSelection(ev xproto.PropertyNotifyEvent) error {
	cont, ok := x.incrs[ev.Window]
	if !ok {
		return fmt.Errorf("could not find INCR to continue: %v", ev)
	}

	if cont.i < 0 {
		// we have finished handling this INCR, clean up
		delete(x.incrs, ev.Window)

		// Stop listening to events on this window
		return x.selectInput(ev.Window, xproto.EventMaskNoEvent)
	}

	remaining := len(cont.data) - cont.i
	dataLen := remaining
	if remaining > x.maxPropSize {
		dataLen = x.maxPropSize
	}

	// First write is a replace, then all subsequent ones including the final 0-len one are appends
	mode := xproto.PropModeAppend
	if cont.i == 0 {
		mode = xproto.PropModeReplace
	}

	err := xproto.ChangePropertyChecked(
		x.conn, byte(mode), ev.Window, cont.property, cont.target,
		8, uint32(dataLen), cont.data[cont.i:cont.i+dataLen],
	).Check()
	if err != nil {
		return fmt.Errorf("could not write property during INCR: %w", err)
	}

	if remaining == 0 {
		// that was the last chunk, stop handling this INCR -- next pass through we will delete it
		cont.i = -1
	} else {
		cont.i += dataLen
	}

	return nil
}

func (x *X) BecomeSelectionOwner() error {
	err := xproto.SetSelectionOwnerChecked(x.conn, x.window, xproto.AtomPrimary, xproto.TimeCurrentTime).Check()
	if err != nil {
		return err
	}
	return xproto.SetSelectionOwnerChecked(x.conn, x.window, x.atoms.clipboard, xproto.TimeCurrentTime).Check()
}

func (x *X) DumpEvent(event *xgb.Event) string {
	v := reflect.ValueOf(*event)
	o := fmt.Sprintf("%s\t", reflect.TypeOf(*event))

	for i := 0; i < v.NumField(); i++ {
		field := v.Field(i)
		name := v.Type().Field(i).Name
		switch name {
		case "Time":
			continue
		case "State":
			state := field.Interface().(uint8)
			v := "NEWVAL"
			if state == xproto.PropertyDelete {
				v = "DELETE"
			}
			o += fmt.Sprintf(" %s=%s", name, v)
		case "Selection", "Property", "Target":
			atomName := x.getAtomName(field.Interface().(xproto.Atom))
			o += fmt.Sprintf(" %s=%s", name, atomName)
		default:
			o += fmt.Sprintf(" %s=%v", name, field.Interface())
		}
	}

	return o
}

func (x *X) selectInput(window xproto.Window, mask uint32) error {
	return xproto.ChangeWindowAttributesChecked(
		x.conn, window, xproto.CwEventMask, []uint32{mask},
	).Check()
}

func (x *X) getAtomName(atom xproto.Atom) string {
	r, err := xproto.GetAtomName(x.conn, atom).Reply()
	if err != nil {
		return fmt.Sprintf("ERR: %s", err)
	}
	return r.Name
}

func (x *X) atomToFormat(atom xproto.Atom) history.ClipFormat {
	if atom == x.atoms.utf8 || atom == xproto.AtomString {
		return history.StringFormat
	}
	if atom == x.atoms.png {
		return history.PngFormat
	}
	return history.NoneFormat
}

func (x *X) formatToAtom(f history.ClipFormat) xproto.Atom {
	if f == history.PngFormat {
		return x.atoms.png
	}
	return xproto.AtomString
}

func createAtom(X *xgb.Conn, n string) xproto.Atom {
	reply, err := xproto.InternAtom(X, false, uint16(len(n)), n).Reply()
	if err != nil {
		return xproto.AtomNone
	}
	return reply.Atom
}

func packInts(ints ...uint32) []byte {
	data := make([]byte, len(ints)*4)
	for i := 0; i < len(ints); i++ {
		xgb.Put32(data[(i*4):], ints[i])
	}
	return data
}

func (x *X) chooseTarget(ev xproto.SelectionNotifyEvent) (xproto.Atom, error) {
	reply, err := xproto.GetProperty(x.conn, true, ev.Requestor, x.atoms.targets, xproto.AtomAtom, 0, (1<<32)-1).Reply()
	if err != nil {
		return xproto.AtomNone, err
	}

	// 32bits per atom, look for our preferred atom type and return it.
	// Adapted from xgbutil/xprop code
	atoms := reply.Value
	for i := 0; len(atoms) >= 4; i++ {
		atom := xproto.Atom(xgb.Get32(atoms))
		if atom == x.atoms.png || atom == x.atoms.utf8 {
			return atom, nil
		}
		atoms = atoms[4:]
	}
	// If we find neither image nor utf8, we default to the string target
	return xproto.AtomString, nil
}
