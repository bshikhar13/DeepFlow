import type { FC } from "react";
import type { CellType } from "../types";
import type { CellProps } from "./common";
import { Concept } from "./Concept";
import { Dialogue } from "./Dialogue";
import { Document } from "./Document";
import { Game } from "./Game";
import { Journey } from "./Journey";
import { MapCard } from "./Map";
import { Quiz } from "./Quiz";
import { Thought } from "./Thought";

export const CellRegistry: Record<CellType, FC<CellProps>> = {
  dialogue: Dialogue, concept: Concept, game: Game, quiz: Quiz, journey: Journey, thought: Thought, document: Document, map: MapCard,
};

export const CELL_TITLES: Record<CellType, string> = {
  dialogue: "Chat", concept: "Key Idea", game: "Game", quiz: "Quiz", journey: "My Journey", thought: "What If?", document: "My Notes", map: "Map",
};

export const CELL_ICONS: Record<CellType, string> = {
  dialogue: "💬", concept: "💡", game: "🎮", quiz: "⚡", journey: "🧭", thought: "🤔", document: "📝", map: "🌍",
};
