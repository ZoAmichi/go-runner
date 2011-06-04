# Copyright 2009 Dimiter Stanev, malkia@gmail.com. All rights reserved.
# Use of this source code is governed by a BSD-style
# license that can be found in the LICENSE file.

include $(GOROOT)/src/Make.inc

TARG    = go
GOFILES = go.go

include $(GOROOT)/src/Make.cmd
