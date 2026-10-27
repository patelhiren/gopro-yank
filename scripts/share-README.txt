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
3. Choose a folder (default: ~/Pictures/GoPro) and confirm the download size.
4. GoPro Yank downloads the available originals and checks every saved file.

Stop whenever you like. Completed files stay completed, and the next run picks
up what remains.


Prefer commands?
----------------

    gopro-yank library
    gopro-yank archive --out /Volumes/Photos/GoPro
    gopro-yank verify --out /Volumes/Photos/GoPro

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
