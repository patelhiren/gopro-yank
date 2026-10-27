GoPro Yank
==========

Bring your GoPro library home. GoPro Yank lets you look through your GoPro
cloud library, choose where it should live, and download all available
originals. Every saved file is checked. Nothing is deleted from GoPro.

Requires a Mac with Apple Silicon (M1 or later).


Open it
-------

Double-click "gopro-yank". It opens in Terminal and walks you through
connecting GoPro, reviewing your library, choosing a folder, and confirming
the download. The first time, macOS asks whether you want to open something
downloaded from the internet; click Open.

Want a harmless tour first? In Terminal:

    ./gopro-yank --demo

That uses sample data and no account.


Keep it around (optional)
-------------------------

To run "gopro-yank" from any Terminal window, move it onto your PATH:

    mkdir -p ~/.local/bin
    mv gopro-yank ~/.local/bin/

If "gopro-yank" is not found afterwards, add ~/.local/bin to your PATH.


How it works
------------

1. Sign in on GoPro's website. GoPro Yank never sees your password.
2. Browse capture dates, media types, total size, what is already archived,
   and what still needs a manual export.
3. Choose a folder (default for the whole library: ~/Pictures/GoPro) and
   confirm the download size.
4. GoPro Yank downloads the available originals and checks every saved file.

Stop whenever you like. Completed files stay completed, and the next run picks
up what remains.


Only part of the library
------------------------

Need just one trip or project? On the library screen, press "s" to choose a
date range and media types. GoPro Yank suggests a folder for that selection in
~/GoPro, such as ~/GoPro/gopro-2026-08-24-to-2026-08-30-video. Files go
straight into one folder per day, such as 2026-08-24/GX011059.MP4.

Dates and times are the ones gopro.com shows, in your computer's time zone.
The folder remembers its selection, so later runs stay inside it.


Prefer commands?
----------------

    gopro-yank library
    gopro-yank archive --out /Volumes/Photos/GoPro
    gopro-yank verify --out /Volumes/Photos/GoPro

Choose part of the library with a capture window and media types. --to
includes the whole day or minute you give it:

    gopro-yank library --from 2026-09-12 --to 2026-09-14 --type Video
    gopro-yank archive --out ~/Movies/tahoe --from 2026-09-12T08:00 --to 2026-09-14T22:00

--tz picks another time zone, --camera-clock matches the camera's own clock
instead, and --all widens a folder back to the whole library.

Move an existing archive into per-day folders without downloading again:

    gopro-yank reorganize --out ~/GoPro/tahoe

Delete a local archive's saved files (asks you to type DELETE, and removes
only the files GoPro Yank recorded; nothing is deleted from GoPro):

    gopro-yank delete --out ~/Movies/tahoe

Run "gopro-yank <command> --help" for options.


Removing it
-----------

Delete the gopro-yank file. Your GoPro login is stored in
~/Library/Application Support/gopro-yank/; delete that folder to forget it.
Downloaded media stays wherever you chose to put it.


More
----

Website: https://gopro-yank.azohra.com
Source:  https://github.com/azohra/gopro-yank (MIT licensed, see LICENSE)

GoPro Yank uses undocumented GoPro cloud endpoints and is intended for your
own account. It is independent open-source software and is not affiliated
with GoPro, Inc.
