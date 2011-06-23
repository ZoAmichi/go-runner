" FILE: go_runner.vim
" Maintainer:	Kai Suzuki (kai.zoamichi@gmail.com)
" Last Change:	2011 June 19

function! GoMake()

  comp! go_runner
  silent execute 'make!'

  let s:qflist = getqflist()
  if len(s:qflist) == 0
    return 0
  endif

  redraw!
  echo "!go -R " . bufname('%')
  for err in s:qflist
    echo bufname(err.bufnr) .':'. err.lnum .':'. err.text
  endfor
  echo "~"

  return 1

endfunction


function! Go(...)

  let args = ""
  for val in a:000
    let args = args . val . " "
  endfor

  if GoMake() == 0
    execute '!go % ' . args
  endif

endfunction

com! GoMake call GoMake()
com! -nargs=* Go call Go(<f-args>)
