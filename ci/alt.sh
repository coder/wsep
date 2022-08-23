#!/usr/bin/env bash
# Script for testing the alt screen.

# Enter alt screen.
tput smcup

function display() {
  # Clear the screen.
  tput clear
  # Move cursor to the top left.
  tput cup 0 0
  # Display content.
  echo "ALT SCREEN"
}

function redraw() {
  display
  echo "redrawn"
}

# Re-display on resize.
trap 'redraw' WINCH

display

# The trap will not run while waiting for a command so read input in a loop with
# a timeout.
while true ; do
  if read -n 1 -t .1 ; then
    # Clear the screen.
    tput clear
    # Exit alt screen.
    tput rmcup
    exit
  fi
done
