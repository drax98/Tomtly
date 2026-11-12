"use client"

import * as React from "react"
import * as SliderPrimitive from "@radix-ui/react-slider"

import { cn } from "@/lib/utils"

const Slider = React.forwardRef<
  React.ElementRef<typeof SliderPrimitive.Root>,
  React.ComponentPropsWithoutRef<typeof SliderPrimitive.Root>
>(({ className, "aria-label": label, ...props }, ref) => (
  <SliderPrimitive.Root
    ref={ref}
    className={cn(
      "relative flex h-8 w-full touch-none select-none items-center data-[disabled]:opacity-50",
      className
    )}
    {...props}
  >
    <SliderPrimitive.Track className="relative h-2 w-full grow overflow-hidden rounded-full bg-primary/20">
      <SliderPrimitive.Range className="absolute h-full bg-primary" />
    </SliderPrimitive.Track>
    {/* The thumb is what has the slider role, so that's where the name has to be. */}
    <SliderPrimitive.Thumb aria-label={label} className="block size-6 rounded-full border-2 border-primary bg-card shadow-sm transition-transform hover:scale-110 motion-reduce:hover:scale-100" />
  </SliderPrimitive.Root>
))
Slider.displayName = SliderPrimitive.Root.displayName

type FollowingSliderProps = Omit<React.ComponentPropsWithoutRef<typeof Slider>, "value" | "defaultValue" | "onValueChange" | "onValueCommit"> & {
  value: number
  onCommit: (value: number) => void
}

// How long a slider waits after the last drag or arrow key before sending, so a few steps send one value.
const COMMIT_DELAY_MS = 2000

/** Calls send with the latest value once calls stop for COMMIT_DELAY_MS, and at once if the component unmounts first. */
function useDebouncedCommit(send: (value: number) => void) {
  const latest = React.useRef<number | null>(null)
  const timer = React.useRef<ReturnType<typeof setTimeout> | undefined>(undefined)
  const sendRef = React.useRef(send)
  sendRef.current = send
  const flush = React.useCallback(() => {
    clearTimeout(timer.current)
    timer.current = undefined
    if (latest.current === null) return
    const value = latest.current
    latest.current = null
    sendRef.current(value)
  }, [])
  React.useEffect(() => flush, [flush]) // closing the page mid-wait still sends the change
  const commit = (value: number) => {
    latest.current = value
    clearTimeout(timer.current)
    timer.current = setTimeout(flush, COMMIT_DELAY_MS)
  }
  const waiting = React.useCallback(() => timer.current !== undefined, [])
  return { commit, waiting }
}

/**
 * A slider's value that follows a device's, except while someone is moving it or a change is waiting to be sent, so a
 * report doesn't throw the thumb back. shown moves at once with the thumb; onCommit gets the value in the background,
 * COMMIT_DELAY_MS after the last step. Spread sliderProps on a Slider.
 */
function useFollowingValue(value: number, onCommit: (value: number) => void) {
  const [shown, setShown] = React.useState(value)
  const dragging = React.useRef(false)
  const { commit, waiting } = useDebouncedCommit(onCommit)
  React.useEffect(() => {
    if (!dragging.current && !waiting()) setShown(value)
  }, [value, waiting])
  const sliderProps = {
    value: [shown],
    onPointerDown: () => (dragging.current = true),
    onPointerUp: () => (dragging.current = false),
    onValueChange: ([v]: number[]) => setShown(v),
    onValueCommit: ([v]: number[]) => commit(v),
  }
  return { shown, sliderProps }
}

/** A Slider on useFollowingValue. It stays mounted when the value changes, so a focused thumb keeps focus. */
function FollowingSlider({ value, onCommit, ...props }: FollowingSliderProps) {
  const { sliderProps } = useFollowingValue(value, onCommit)
  return <Slider {...props} {...sliderProps} />
}

export { Slider, FollowingSlider, useFollowingValue }
