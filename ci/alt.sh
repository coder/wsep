#!/usr/bin/env bash
# Script for testing the alt screen.

# Enter alt screen.
printf '\033[?1049h'

# Move cursor to the top left.
printf '\033[0;0f'

function display() {
  echo "ALT SCREEN"
}

# Re-display on resize.
trap 'display' WINCH

display

# The trap will not run while waiting for a command so read input in a loop with
# a timeout.
while true ; do
  # Exit alt screen on input.
  if read -n 1 -t .1 ; then
    printf '\033[?1049l'
    exit
  fi
done
