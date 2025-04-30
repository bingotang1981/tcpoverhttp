# tcp over http
It is based on the good work from https://github.com/shawn246/tcp-over-http. The relevant enhancements are listed as follows:
(1) Make the logic clear for both the client site and server side.
(2) Provide the AES 256 encryption.
(3) Support SSE (Server Side Event) mode if there is no response for 90 seconds
