package page

// A comment mentioning flex, grid. and fn(hidden) only counts "flex".
var classes = h.Div(h.Class("inline-flex items-center gap-2 rounded-md px-4 has-[>svg]:px-3 [&_svg:not([class*='size-'])]:size-4"),
	h.Class("hover:bg-primary/90 focus-visible:ring-[3px] dark:bg-input/30 bg-brand content-auto"),
	h.Class("data-[state=open]:animate-in md:grid-cols-3 -mt-2 p-4! !m-1 w-[calc(100%-2rem)] bg-(--my-color)"))
