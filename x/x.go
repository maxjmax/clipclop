package x

import (
	"fmt"

	"github.com/BurntSushi/xgb"
	"github.com/BurntSushi/xgb/xfixes"
	"github.com/BurntSushi/xgb/xproto"
	"github.com/maxjmax/clipclop/history"
)

type atoms struct {
	selectionProperty xproto.Atom
	clipboard         xproto.Atom
	targets           xproto.Atom
	incr              xproto.Atom
	png               xproto.Atom
	utf8              xproto.Atom
}

type X struct {
	conn   *xgb.Conn
	window *xproto.Window
	screen *xproto.ScreenInfo
	atoms  atoms
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

	// TODO: eh, not pretty, don't want to use a map
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
		conn:   conn,
		screen: screen,
		atoms:  atoms,
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

	// We can still handle events without mapping (showing) the window
	// xproto.MapWindowChecked(X, wid).Check()

	// Request events to it when the selection changes
	var mask uint32 = xfixes.SelectionEventMaskSetSelectionOwner
	if err = xfixes.SelectSelectionInputChecked(x.conn, wid, xproto.AtomPrimary, mask).Check(); err != nil {
		return fmt.Errorf("could not select primary selection events: %w", err)
	}

	if err = xfixes.SelectSelectionInputChecked(x.conn, wid, x.atoms.clipboard, mask).Check(); err != nil {
		return fmt.Errorf("could not select clipboard selection events: %w", err)
	}

	x.window = &wid
	return nil
}

func (x *X) NextEvent() (xgb.Event, xgb.Error) {
	return x.conn.WaitForEvent()
}

func (x *X) ConvertSelection(ev xfixes.SelectionNotifyEvent) error {
	if ev.Owner == *x.window {
		return nil // that was us
	}

	return xproto.ConvertSelectionChecked(
		x.conn, ev.Window, ev.Selection, x.atoms.targets, x.atoms.targets, ev.SelectionTimestamp).Check()
}

// TODO: shouldn't be using history formats here?
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

func (x *X) SetSelection(ev xproto.SelectionRequestEvent, data *[]uint8, format history.ClipFormat) error {

	// TODO: if too big, we need to set the type of the property to INCR and set the value to the number of bytes total.
	// then we need to go back to the loop, -- which means we probably need to have some global state (well, in X) corresponding to
	// the thing currently being sent.

	var err error
	if ev.Target == x.atoms.targets {
		data := make([]byte, 8)
		xgb.Put32(data, uint32(x.formatToAtom(format)))
		xgb.Put32(data[4:], uint32(x.atoms.targets))
		err = xproto.ChangePropertyChecked(x.conn, xproto.PropModeReplace, ev.Requestor, ev.Property, xproto.AtomAtom, 32, 2, data).Check()
	} else {
		// target := x.formatToAtom(currentClip.format)
		target := ev.Target
		data := []byte(*data)
		err = xproto.ChangePropertyChecked(x.conn, xproto.PropModeReplace, ev.Requestor, ev.Property, target, 8, uint32(len(data)), data).Check()
	}

	if err != nil {
		return err
	}

	notifyEvent := xproto.SelectionNotifyEvent{
		Sequence:  ev.Sequence + 1,
		Time:      ev.Time,
		Requestor: ev.Requestor,
		Selection: ev.Selection,
		Target:    ev.Target, // TARGETS or whatever they requested
		Property:  ev.Property,
	}
	// return xproto.SendEventChecked(X, false, ev.Requestor, xproto.SelectionNotify, string(notifyEvent.Bytes())).Check()
	return xproto.SendEventChecked(x.conn, false, ev.Requestor, xproto.EventMaskNoEvent, string(notifyEvent.Bytes())).Check()
}

func (x *X) BecomeSelectionOwner() error {
	err := xproto.SetSelectionOwnerChecked(x.conn, *x.window, xproto.AtomPrimary, xproto.TimeCurrentTime).Check()
	if err != nil {
		return err
	}
	return xproto.SetSelectionOwnerChecked(x.conn, *x.window, x.atoms.clipboard, xproto.TimeCurrentTime).Check()
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
