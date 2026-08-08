import { Button } from "./Button";

test("Button renders", () => {
  expect(Button({ children: "Save" })).toBeTruthy();
});
