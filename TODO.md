# For v1

https://www.x.org/releases/X11R7.6/doc/xorg-docs/specs/ICCCM/icccm.html

## Next

- TODO Pasting an image of ~490kB into an email gives this error:
    `clipclop[2243040]: 2022/10/03 08:55:19 main.go:113: Could not set selection for requestor: BadLength {NiceName: Length, Sequence: 106, BadValue: 8388608, MinorOpcode: 0, MajorOpcode: 18}`
    Guessing too large to do in one go? Need to support INCR?
    https://www.x.org/releases/X11R7.6/doc/xorg-docs/specs/ICCCM/icccm.html#incr_properties

        Requestors may receive a property of type INCR [6] in response to any target that results in selection data.

        This indicates that the owner will send the actual data incrementally. The contents of the INCR property will be an integer, which represents a lower bound on the number of bytes of data in the selection. The requestor and the selection owner transfer the data in the selection in the following manner.

        The selection requestor starts the transfer process by deleting the (type==INCR) property forming the reply to the selection.

        The selection owner then:

        Appends the data in suitable-size chunks to the same property on the same window as the selection reply with a type corresponding to the actual type of the converted selection. The size should be less than the maximum-request-size in the connection handshake.

        Waits between each append for a PropertyNotify (state==Deleted) event that shows that the requestor has read the data. The reason for doing this is to limit the consumption of space in the server.

        Waits (after the entire data has been transferred to the server) until a PropertyNotify (state==Deleted) event that shows that the data has been read by the requestor and then writes zero-length data to the property.

        The selection requestor:

        Waits for the SelectionNotify event.

        Loops:

        Retrieving data using GetProperty with the delete argument True.

        Waiting for a PropertyNotify with the state argument NewValue.

        Waits until the property named by the PropertyNotify event is zero-length.

        Deletes the zero-length property.

        The type of the converted selection is the type of the first partial property. The remaining partial properties must have the same type.

     see: https://github.com/kfish/xsel/blob/master/xsel.c#L1275 check that
     looks straight forward actually. State is going to be annoying to handle.

    setup.MaximumRequestLength * 4 bytes is the most we could set, which will be 256kB pretty much everywhere. Want to use INCR for things bigger than that.

## Image support

- TODO image selection depends on size, currently. Two images of identical size will result in the first being picked always.
    This is also an issue for strings with the same first line, but would be hard to get around unless we also:
        - check the time
        - prefix with an index 01 [5s ago] Blah blah blah
    This would be easier if dmenu could report the index of the chosen item, but it doesn't do that without a patch.

- TODO Pasting images into google keep results in an error (complaining about size/format).

- TODO image blob stored in file if large? same for large text clips?

## HTML/Rich text support

- TODO support rich text/html copy pasting

## Builds

- TODO screenshot
- TODO integration testing, somehow -- would be good to fuzz with random clips then select n'th, check we get full content back, etc.
    could use xclip perhaps to simulate setting clipboard, + xsel to check contents is set correctly
- TODO better readme with setup instructions
- TODO automatic release builds
- TODO: also run go vet, https://staticcheck.io/docs/running-staticcheck/ci/github-actions/ etc. on push.
- TODO submit to aur

# Later

## Unicode testing

Probably bugs here

- strings are truncated to the wrong length with multicharacter runes
- strings might be truncated in the middle of a rune

## Persistence

serialise to disk + resume on restart https://pkg.go.dev/encoding/gob
on each copy? cheaper and nicer to append to a text file and do ocassional vaccum?
encode newlines then use newline as a separator? -- https://pkg.go.dev/encoding/base64@go1.19.1
fuzztest that does some roundtrips
vaccum = copy the last 50 lines to a new file and then unlink the old file I guess

If we _are_ only appending, then duplicates will be appended too. Either we leave far more in the file so that we
can drop them when we vaccum, or we only persist the _previous_ clip? (or we wait for 15s before we append), so that
we know that we won't need to replace the previous line in the file.

Or.. we just persist the whole damn thing every now and then (15s of no activity?). This is probably good enough, right? This isn't
critical.

## Show the source window name (configurable format string?)

"[Chrome 8s ago] Blah blah [+ 1 lines]"