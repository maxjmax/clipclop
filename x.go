package main

import (
	"errors"
	"fmt"

	"github.com/BurntSushi/xgb"
	"github.com/BurntSushi/xgb/xfixes"
	"github.com/BurntSushi/xgb/xproto"
)

type Atoms struct {
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
	atoms  Atoms
}

func startX() (*X, error) {
	conn, err := xgb.NewConn()
	if err != nil {
		return nil, fmt.Errorf("Could not create X connection: %w", err)
	}

	if err = xfixes.Init(conn); err != nil {
		return nil, fmt.Errorf("Could not init xfixes extension : %w", err)
	}

	setup := xproto.Setup(conn)
	screen := setup.DefaultScreen(conn)

	// From https://www.x.org/releases/current/doc/fixesproto/fixesproto.txt
	// The client must negotiate the version of the extension before executing
	// extension requests.  Behavior of the server is undefined otherwise.
	if _, err = xfixes.QueryVersion(conn, 2, 0).Reply(); err != nil {
		return nil, fmt.Errorf("Could not negotiate xfixes version: %w", err)
	}

	// TODO: eh, not pretty, don't want to use a map
	atoms := Atoms{
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
		return nil, errors.New(fmt.Sprintf("Could not create atom: %v", atoms))
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
		return fmt.Errorf("Could not get window ID: %w", err)
	}

	err = xproto.CreateWindowChecked(
		x.conn, x.screen.RootDepth, wid, x.screen.Root, 0, 0, 1, 1, 0,
		xproto.WindowClassInputOutput, x.screen.RootVisual,
		0, []uint32{},
	).Check()
	if err != nil {
		return fmt.Errorf("Could not create event window : %w", err)
	}

	// We can still handle events without mapping (showing) the window
	// xproto.MapWindowChecked(X, wid).Check()

	// Request events to it when the selection changes
	var mask uint32 = xfixes.SelectionEventMaskSetSelectionOwner
	if err = xfixes.SelectSelectionInputChecked(x.conn, wid, xproto.AtomPrimary, mask).Check(); err != nil {
		return fmt.Errorf("Could not select primary selection events: %w", err)
	}

	if err = xfixes.SelectSelectionInputChecked(x.conn, wid, x.atoms.clipboard, mask).Check(); err != nil {
		return fmt.Errorf("Could not select clipboard selection events: %w", err)
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

func (x *X) GetSelection(ev xproto.SelectionNotifyEvent) (error, []uint8, Format) {
	if ev.Property == x.atoms.targets {
		target, err := x.chooseTarget(ev)
		if err != nil {
			return fmt.Errorf("Failed to choose target: %w", err), nil, NoneFormat
		}
		err = xproto.ConvertSelectionChecked(x.conn, ev.Requestor, ev.Selection, target, x.atoms.selectionProperty, ev.Time).Check()
		if err != nil {
			return fmt.Errorf("Error requesting selection convert to %d, %w", target, err), nil, NoneFormat
		}
	} else {
		reply, err := xproto.GetProperty(x.conn, true, ev.Requestor, x.atoms.selectionProperty, ev.Target, 0, (1<<32)-1).Reply()
		if err != nil {
			return fmt.Errorf("Failed to get selection prop: %w", err), nil, NoneFormat
		} else if len(reply.Value) > 0 {
			return nil, reply.Value, x.atomToFormat(ev.Target)
		}
	}
	// Empty selection
	return nil, nil, NoneFormat
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

func (x *X) SetSelection(ev xproto.SelectionRequestEvent, data *[]uint8, format Format) error {
	// TODO: debug
	requestedTargetReply, err := xproto.GetAtomName(x.conn, ev.Target).Reply()
	if err != nil {
		return err
	} else {
		logger.Printf("Selection was requested in format %s", requestedTargetReply.Name)
	}

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

func (x *X) atomToFormat(atom xproto.Atom) Format {
	if atom == x.atoms.utf8 || atom == xproto.AtomString {
		return StringFormat
	}
	if atom == x.atoms.png {
		return PngFormat
	}
	logger.Fatalf("Invalid format atom %d", atom)
	return 0
}

func (x *X) formatToAtom(f Format) xproto.Atom {
	if f == PngFormat {
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
