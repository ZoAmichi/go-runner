" Vim compiler file
" Compiler:		go_runner
" Maintainer:	Kai Suzuki (kai.zoamichi@gmail.com)
" Last Change:	2011 June 19

if exists("current_compiler")
  finish
endif
let current_compiler = "go_runner"
  
let s:savecpo = &cpo
set cpo&vim
  
if exists(":CompilerSet") != 2  " older Vim always used :setlocal
  command -nargs=* CompilerSet setlocal <args>
endif

CompilerSet makeprg=go\ -Rq\ %
CompilerSet errorformat=%f:%l:%m

let &cpo = s:savecpo
unlet s:savecpo
