# For v1

https://www.x.org/releases/X11R7.6/doc/xorg-docs/specs/ICCCM/icccm.html

## Next

- TODO handle C-c nicely, signal.NotifyContext

- TODO vim selection no longer sycronising with system clipboard on my setup

- TODO Add integration tests for png target

## Image support

- TODO image selection depends on size, currently. Two images of identical size will result in the first being picked always.
    This is also an issue for strings with the same first line, but would be hard to get around unless we also:
        - check the time
        - prefix with an index 01 [5s ago] Blah blah blah
    This would be easier if dmenu could report the index of the chosen item, but it doesn't do that without a patch.

- TODO image blob stored in file if large? same for large text clips?

## HTML/Rich text support

- TODO support rich text/html copy pasting

## Builds

- TODO better readme with setup instructions
- TODO automatic release builds
- TODO submit to aur

# Later

## Unicode testing

Probably bugs here

- strings are truncated to the wrong length with multicharacter runes

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