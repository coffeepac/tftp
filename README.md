In-memory TFTP Server
=====================

This is a simple in-memory TFTP server, implemented in Go.  It is
RFC1350-compliant, but doesn't implement the additions in later RFCs.  In
particular, options are not recognized.

Usage
-----
To build:
- cd into project root directory
- execute `go build -o tftpd`

To run:
- follow steps to build
- cd into project root directory
- execute `./tftpd`

This server listens on port 9010 which is unprivledged so there is no need to 
execute this server as root.  The transaction log will be created in the 
project root directory with name `tftpTxn.log`.  This server will also write
out all file names that have been stored to STDOUT when killed with CTRL-C.

Testing
-------
Unit tests exist for generating connection on ephemeral port, error packet 
responses and the retry logic for a RRQ.  I belive this is demonstrative of 
how unit tests should look:  create test versions of the API interfaces and 
manually excercise the server code.  Things that should also be tested but 
I've run out of time:
- retry logic for a RWQ (same function as RRQ but slightly different 
  expectations)
- RRQ end-to-end using MockPacketConn
  - this should include mocking things like:
    - unexpected TID during a retry loop 
    - jumbled ACKs
- RWQ end-to-end using MockPacketConn
  - this should include mocking things like:
    - unexpected TID during a retry loop 
    - jumbled ACKs

There are no integration tests.  If time permitted I would have built a bash
script that wrote several files and read them back to make sure there was no
corruption.  I also would have written at least one very large file over a
locally port forwarded connection.  While the large file is transferring I 
could 
- drop and rebuild the connection to ensure things function
- send out-of-order ACKs and unknown TIDs
This would be done to make sure the entire application functions under duress.


Product Roadmap
---------------
- When exiting via CTRL-C, send a notification to all running go routines to 
  gracefully terminate their connections at the next available moment
- Create instructions for generating a user that only has the ability to open 
  port 69 and is otherwise limited to almost nothing
- Configfile or command line arguments.  At a minimum these would allow for
  control over the global variables
- CI configs (circleCI or travis is open source, jenkins or github if self
  hosted)